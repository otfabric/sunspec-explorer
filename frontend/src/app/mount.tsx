import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '../styles/index.css';
import { App } from './App';
import type { Platform } from './platform/platform';
import type { Service } from './service/service';

/** Renders the Explorer; the entry points differ only in what they pass here. */
export function mount(service: Service, platform: Platform): void {
  createRoot(document.getElementById('root') as HTMLElement).render(
    <StrictMode>
      <App service={service} platform={platform} />
    </StrictMode>,
  );
}
