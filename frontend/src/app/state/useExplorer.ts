import { useCallback, useEffect, useMemo, useReducer } from 'react';
import { estimateSkewMs } from './engine';
import { toServiceError, type Service } from '../service/service';
import type { ExportFile, ExportFormat, RecordingFormat, Snapshot, TargetConfig } from '../service/types';
import { initialState, reduce, type Selection } from './store';

/**
 * Connects the reducer to a Service. State changes only through Service
 * results, the event stream and user actions: there is no timer here.
 */
export function useExplorer(service: Service) {
  const [state, dispatch] = useReducer(reduce, initialState);
  const sessionId = state.session?.id;
  const { wantFull, wantState, scope } = state;
  const recordingId = state.recording?.id;

  const report = useCallback((err: unknown) => {
    const error = toServiceError(err);
    // A refused token is shown as the state of the link, not as a notice.
    if (error.code !== 'unauthorized') {
      dispatch({ type: 'notice', message: error.message });
    }
  }, []);

  // Versions, features and the clock of the engine. Asked at startup and
  // again whenever a lost link is back: the engine may have been replaced.
  const checkHealth = useCallback(() => {
    const sentAt = Date.now();
    service
      .health()
      .then((health) => {
        dispatch({ type: 'health', health, skewMs: estimateSkewMs(health.time, sentAt, Date.now()) });
        return health.features.includes('serial') ? service.serialPorts() : [];
      })
      .then((ports) => dispatch({ type: 'serialPorts', ports }))
      .catch(report);
  }, [service, report]);

  useEffect(() => {
    let current = true;
    let wasDown = false;
    const unsubscribe = service.subscribe(
      0,
      (event) => dispatch({ type: 'event', event }),
      (status) => {
        dispatch({ type: 'link', status });
        if (status === 'up' && wasDown) {
          checkHealth();
        }
        wasDown = status === 'down';
      },
    );
    checkHealth();
    // A daemon keeps its session while no window is looking: a tab that is
    // opened or reloaded picks up the one that is there.
    service
      .sessions()
      .then(([open]) => open && service.state(open.id))
      .then((attached) => {
        if (attached && current) {
          dispatch({ type: 'attached', state: attached });
        }
      })
      .catch(report);
    return () => {
      current = false;
      unsubscribe();
    };
  }, [service, report, checkHealth]);

  // After a connect, a chain change or a sample whose shape is new, fetch the
  // affected models with full metadata.
  useEffect(() => {
    if (sessionId === undefined || wantFull.length === 0) {
      return;
    }
    dispatch({ type: 'fullRequested' });
    service
      .snapshot(sessionId, { model_indexes: wantFull, metadata: 'full' })
      .then((snapshot) => dispatch({ type: 'snapshot', snapshot }))
      .catch((err: unknown) => {
        dispatch({ type: 'fullFailed', indexes: wantFull });
        report(err);
      });
  }, [service, sessionId, wantFull, report]);

  const refreshState = useCallback(
    (id: string) =>
      service
        .state(id)
        .then((fetched) => dispatch({ type: 'state', state: fetched }))
        .catch((err: unknown) => {
          if (toServiceError(err).code === 'not_found') {
            dispatch({ type: 'disconnected' });
            dispatch({ type: 'notice', message: 'The session no longer exists on the engine. Connect again.' });
          } else {
            dispatch({ type: 'stateFailed' });
            report(err);
          }
        }),
    [service, report],
  );

  useEffect(() => {
    if (sessionId !== undefined && wantState) {
      void refreshState(sessionId);
    }
  }, [sessionId, wantState, refreshState]);

  // Stable, so that a view can call it once when it opens.
  const refreshRecordings = useCallback(() => {
    service
      .recordings()
      .then((recordings) => dispatch({ type: 'recordings', recordings }))
      .catch(report);
  }, [service, report]);

  return useMemo(() => {
    const withSession = (work: (id: string) => Promise<unknown>) => {
      if (sessionId !== undefined) {
        work(sessionId).catch(report);
      }
    };
    return {
      state,
      connect(cfg: TargetConfig) {
        dispatch({ type: 'connecting' });
        service
          .openSession(cfg)
          .then((session) => dispatch({ type: 'discovered', session }))
          .catch((err: unknown) => dispatch({ type: 'connectFailed', error: toServiceError(err) }));
      },
      disconnect() {
        withSession(async (id) => {
          dispatch({ type: 'disconnected' });
          await service.closeSession(id);
        });
      },
      rediscover() {
        withSession(async (id) => dispatch({ type: 'discovered', session: await service.rediscover(id) }));
      },
      readOnce() {
        withSession(async (id) => {
          dispatch({ type: 'reading', reading: true });
          dispatch({ type: 'snapshot', snapshot: await service.snapshot(id, { model_indexes: scope, metadata: 'full' }) });
        });
      },
      startPolling(intervalS: number) {
        withSession(async (id) => {
          await service.startPolling(id, { interval_s: intervalS, model_indexes: scope });
          await refreshState(id);
        });
      },
      stopPolling() {
        withSession(async (id) => {
          await service.stopPolling(id);
          await refreshState(id);
        });
      },
      /** Reads every model once more, so the export is complete and current. */
      async exportSnapshot(): Promise<Snapshot | null> {
        if (sessionId === undefined) {
          return null;
        }
        const snapshot = await service.snapshot(sessionId, { metadata: 'full' });
        dispatch({ type: 'snapshot', snapshot });
        return snapshot;
      },
      startRecording(name: string) {
        withSession(async (id) => dispatch({ type: 'recording', recording: await service.startRecording(id, name === '' ? {} : { name }) }));
      },
      stopRecording() {
        if (recordingId !== undefined) {
          service
            .stopRecording(recordingId)
            .then((recording) => dispatch({ type: 'recording', recording }))
            .catch(report);
        }
      },
      refreshRecordings,
      deleteRecording(id: string) {
        service
          .deleteRecording(id)
          .then(() => dispatch({ type: 'recordingDeleted', id }))
          .catch(report);
      },
      saveRecording(id: string, format: RecordingFormat) {
        service.saveRecording(id, format).catch(report);
      },
      /** Has the engine read the device and render it as a file. */
      async exportSession(format: ExportFormat): Promise<ExportFile | null> {
        return sessionId === undefined ? null : service.exportSession(sessionId, format);
      },
      dismissSkew() {
        dispatch({ type: 'dismissSkew' });
      },
      dismissRecording() {
        dispatch({ type: 'dismissRecording' });
      },
      select(selected: Selection) {
        dispatch({ type: 'select', selected });
      },
      toggleScope(index: number) {
        dispatch({ type: 'toggleScope', index });
      },
      report,
      /** Asks the engine again what it can do, after something changed that. */
      refreshHealth: checkHealth,
      dismissRestored() {
        dispatch({ type: 'dismissRestored' });
      },
      dismissNotice() {
        dispatch({ type: 'notice', message: null });
      },
    };
  }, [state, service, sessionId, recordingId, scope, report, refreshState, refreshRecordings, checkHealth]);
}

export type Explorer = ReturnType<typeof useExplorer>;
