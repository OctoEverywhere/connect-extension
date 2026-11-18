import type { Operation } from '@prusa/connect';
import type { ModuleHost } from '@prusa/connect-ui/slots';

export interface PrinterSettings {
  enabled: boolean;
  camera_id: string;
  auto_pause: boolean;
  warning_confidence: number;
  pause_confidence: number;
  warmup_seconds: number;
}

export interface Printer extends PrinterSettings {
  id: string;
  name: string;
  state: string;
  can_manage: boolean;
  cameras: { id: string; name: string }[];
  last_inspection_at?: string;
  quality?: number;
  warning?: boolean;
  pause_suggested?: boolean;
  job_id?: string;
  job_name?: string;
  progress?: number;
  detail?: string;
}

export interface HistoryEntry {
  at: string;
  printer_id: string;
  printer_name: string;
  job_name?: string;
  kind: string;
  message: string;
  quality?: number;
}

export interface Status {
  configured: boolean;
  camera_configured: boolean;
  connect_url: string;
  can_manage: boolean;
  error?: string;
  printers: Printer[];
  history: HistoryEntry[];
}

const prefix =
  '/api/v1/modules/com.github.aux.octoeverywhere.connect-extension';

const operation = (
  id: string,
  method: Operation['method'],
  path: string,
): Operation => ({
  id,
  method,
  path: `${prefix}${path}`,
  response: 'json',
  anonymous: false,
  ...(method !== 'GET' && { body: 'application/json' }),
});

// Use the host's authenticated transport. No browser-held Connect credential
// or independent networking client is created by the extension.
export const apiOn = (host: ModuleHost) => ({
  status: (signal?: AbortSignal) =>
    host.client.transport.call<Status>(
      operation('read-status', 'GET', '/status'),
      {},
      signal,
    ),
  saveKey: (apiKey: string, signal?: AbortSignal) =>
    host.client.transport.call(
      operation('save-gadget', 'POST', '/settings/gadget'),
      { body: { api_key: apiKey } },
      signal,
    ),
  saveConnect: (url: string, token: string, signal?: AbortSignal) =>
    host.client.transport.call(
      operation('save-connect', 'POST', '/settings/connect'),
      { body: { url, token } },
      signal,
    ),
  savePrinter: (id: string, settings: PrinterSettings, signal?: AbortSignal) =>
    host.client.transport.call(
      operation('save-printer', 'PUT', '/printers/{id}'),
      { path: { id }, body: settings },
      signal,
    ),
  testCamera: (id: string, signal?: AbortSignal) =>
    host.client.transport.call<{ ok: boolean }>(
      operation('test-camera', 'POST', '/printers/{id}/test-camera'),
      { path: { id }, body: {} },
      signal,
    ),
});

export const printerSettings = (printer: Printer): PrinterSettings => ({
  enabled: printer.enabled,
  camera_id: printer.camera_id,
  auto_pause: printer.auto_pause,
  warning_confidence: printer.warning_confidence,
  pause_confidence: printer.pause_confidence,
  warmup_seconds: printer.warmup_seconds,
});
