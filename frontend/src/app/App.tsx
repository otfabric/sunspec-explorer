import { useMemo, useState } from 'react';
import { ConfirmDialog } from './components/ConfirmDialog';
import { ConnectError } from './components/ConnectError';
import { ConnectionBar } from './components/ConnectionBar';
import { Header } from './components/Header';
import { LinkBanner, NoToken } from './components/LinkStatus';
import { ModelSummary } from './components/ModelSummary';
import { PointDetail } from './components/PointDetail';
import { PointTable } from './components/PointTable';
import { RawRegisters } from './components/RawRegisters';
import { RecordingBar } from './components/RecordingBar';
import { RecordingsDialog } from './components/RecordingsDialog';
import { Sidebar } from './components/Sidebar';
import { StartRecordingDialog } from './components/StartRecordingDialog';
import { StatusBar } from './components/StatusBar';
import { TableFilters } from './components/TableFilters';
import { Toolbar } from './components/Toolbar';
import { Button } from './components/ui';
import { ErrorIcon } from './icons';
import type { Platform } from './platform/platform';
import type { Service } from './service/service';
import type { ModelResult } from './service/types';
import { flattenModels, type PointRow } from './state/rows';
import { useExplorer } from './state/useExplorer';
import { useTheme } from './state/useTheme';

interface AppProps {
  service: Service;
  platform: Platform;
}

const exportName = (capturedAt: string) => `sunspec-snapshot-${capturedAt.slice(0, 19).replace(/[-:]/g, '')}Z.json`;

export function App({ service, platform }: AppProps) {
  const explorer = useExplorer(service);
  const { state } = explorer;
  const { session, results, selected, recording } = state;
  const recordingRuns = recording?.state === 'running';
  const [theme, toggleTheme] = useTheme(platform);
  const [query, setQuery] = useState('');
  const [showRaw, setShowRaw] = useState(true);
  const [showDocAddress, setShowDocAddress] = useState(false);
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [confirmingExport, setConfirmingExport] = useState(false);
  const [namingRecording, setNamingRecording] = useState(false);
  const [showingRecordings, setShowingRecordings] = useState(false);

  const selectedModel = selected === 'all' ? undefined : session?.models.find((model) => model.index === selected);
  const selectedResult = selectedModel && results[selectedModel.index];
  const rows = useMemo(() => {
    const shown = selected === 'all' ? Object.values(results).sort((a, b) => a.index - b.index) : [results[selected]].filter((r): r is ModelResult => r !== undefined);
    return flattenModels(shown, query, selected === 'all');
  }, [results, selected, query]);
  const activeRow = rows.find((row): row is PointRow => row.kind === 'point' && row.key === activeKey) ?? null;
  const pointCount = useMemo(() => rows.filter((row) => row.kind === 'point').length, [rows]);
  // Without the engine nothing can be read or changed; what is on screen stays.
  const offline = state.link !== 'up';
  const showRegisters = selectedResult !== undefined && selectedResult.group === null && selectedResult.raw_registers !== null;

  async function exportSnapshot() {
    setConfirmingExport(false);
    try {
      const snapshot = await explorer.exportSnapshot();
      if (snapshot) {
        await platform.saveFile(exportName(snapshot.captured_at), 'application/json', JSON.stringify(snapshot, null, 2));
      }
    } catch (err) {
      explorer.report(err);
    }
  }

  return (
    <div className="flex h-full flex-col">
      <Header mode={platform.mode} theme={theme} onToggleTheme={toggleTheme} />
      <ConnectionBar
        connected={session !== null}
        connecting={state.connecting}
        offline={offline}
        onConnect={explorer.connect}
        onDisconnect={explorer.disconnect}
      />
      <LinkBanner link={state.link} restored={state.restored} recording={recordingRuns} onDismiss={explorer.dismissRestored} />
      {state.notice && (
        <div className="flex shrink-0 items-center gap-2 border-b border-danger px-4 py-2 text-danger" role="alert">
          <ErrorIcon />
          <span className="flex-1 font-mono text-[13px]">{state.notice}</span>
          <Button onClick={explorer.dismissNotice}>Dismiss</Button>
        </div>
      )}
      {recording && (
        <RecordingBar
          recording={recording}
          polling={state.polling}
          offline={offline}
          onSave={() => explorer.saveRecording(recording.id)}
          onDismiss={explorer.dismissRecording}
        />
      )}
      <div className="flex min-h-0 flex-1">
        {state.link === 'unauthorized' ? (
          <main className="min-w-0 flex-1 overflow-auto">
            <NoToken />
          </main>
        ) : session ? (
          <>
            <Sidebar
              session={session}
              results={results}
              selected={selected}
              scope={state.scope}
              onSelect={explorer.select}
              onToggleScope={explorer.toggleScope}
              onRediscover={explorer.rediscover}
              offline={offline}
            />
            <main className="flex min-w-0 flex-1 flex-col">
              <Toolbar
                polling={state.polling}
                reading={state.reading}
                scopeSize={state.scope.length}
                offline={offline}
                onReadOnce={explorer.readOnce}
                onStartPolling={explorer.startPolling}
                onStopPolling={explorer.stopPolling}
                onExport={() => setConfirmingExport(true)}
                recording={recordingRuns}
                onRecord={() => setNamingRecording(true)}
                onStopRecording={explorer.stopRecording}
              />
              {selectedModel && <ModelSummary model={selectedModel} result={selectedResult} />}
              {showRegisters ? (
                <RawRegisters model={selectedResult} />
              ) : (
                <>
                  <TableFilters
                    query={query}
                    onQuery={setQuery}
                    showRaw={showRaw}
                    onShowRaw={setShowRaw}
                    showDocAddress={showDocAddress}
                    onShowDocAddress={setShowDocAddress}
                    matchCount={pointCount}
                  />
                  <PointTable rows={rows} showRaw={showRaw} showDocAddress={showDocAddress} activeKey={activeKey} onActivate={setActiveKey} />
                  <PointDetail row={activeRow} onCopy={(text) => platform.copyText(text).catch(explorer.report)} />
                </>
              )}
            </main>
          </>
        ) : (
          <main className="min-w-0 flex-1 overflow-auto">
            {state.connectError ? (
              <ConnectError error={state.connectError} />
            ) : (
              <p className="m-4 max-w-xl text-muted">
                Enter the URL and unit ID of a SunSpec device and connect. The Explorer only reads: it never writes to a device.
              </p>
            )}
          </main>
        )}
      </div>
      <StatusBar health={state.health} link={state.link} session={session} connecting={state.connecting} polling={state.polling}
        results={results}
        recording={recording}
        onShowRecordings={() => setShowingRecordings(true)}
      />
      {namingRecording && (
        <StartRecordingDialog
          polling={state.polling.active}
          onCancel={() => setNamingRecording(false)}
          onStart={(name) => {
            setNamingRecording(false);
            explorer.startRecording(name);
          }}
        />
      )}
      {showingRecordings && (
        <RecordingsDialog
          recordings={state.recordings}
          offline={offline}
          onRefresh={explorer.refreshRecordings}
          onSave={explorer.saveRecording}
          onDelete={explorer.deleteRecording}
          onClose={() => setShowingRecordings(false)}
        />
      )}
      {confirmingExport && (
        <ConfirmDialog title="Export snapshot" confirmLabel="Export" onConfirm={exportSnapshot} onCancel={() => setConfirmingExport(false)}>
          <p>
            The export contains the device serial number and network addresses. Share the file only with people who may know them.
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}
