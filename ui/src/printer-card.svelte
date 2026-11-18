<script lang="ts">
  import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';
  import {
    Button,
    Checkbox,
    FaultAlert,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
  } from '@prusa/connect-ui/primitives';
  import {
    apiOn,
    printerSettings,
    type Printer,
    type PrinterSettings,
  } from './api';
  import { heldHost } from './host';

  let {
    printer,
    ready,
    onrefresh,
  }: { printer: Printer; ready: boolean; onrefresh: () => Promise<void> } =
    $props();
  const host = heldHost();
  const t = host.t;
  const api = apiOn(host);
  let expanded = $state(false);
  let draft = $state<PrinterSettings | undefined>();
  let busy = $state<'save' | 'test' | undefined>();
  let fault = $state<FaultText | undefined>();
  let notice = $state('');
  const settings = $derived(draft ?? printerSettings(printer));
  const cameraItems = $derived(
    printer.cameras.map((camera) => ({ value: camera.id, label: camera.name })),
  );
  const statusTone = $derived.by(() => {
    if (
      printer.warning ||
      printer.pause_suggested ||
      ['error', 'quota', 'no_camera'].includes(printer.state)
    )
      return 'warning';
    if (printer.state === 'monitoring') return 'good';
    return 'neutral';
  });

  function change(patch: Partial<PrinterSettings>) {
    draft = { ...settings, ...patch };
    notice = '';
  }

  async function run(action: 'save' | 'test') {
    if (busy || !printer.can_manage) return;
    busy = action;
    fault = undefined;
    notice = '';
    try {
      if (action === 'save') {
        await api.savePrinter(printer.id, settings, host.signal);
        draft = undefined;
        notice = t('printer.saved');
      } else {
        await api.testCamera(printer.id, host.signal);
        notice = t('printer.cameraPassed');
      }
      await onrefresh();
    } catch (thrown: unknown) {
      if (!host.signal.aborted) fault = describeFault(thrown);
    } finally {
      busy = undefined;
    }
  }

  function inspectionTime(value: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime())
      ? t('common.unavailable')
      : new Intl.DateTimeFormat(undefined, {
          hour: 'numeric',
          minute: '2-digit',
        }).format(date);
  }
</script>

<article class="oe-card oe-printer" aria-labelledby={`printer-${printer.id}`}>
  <div class="oe-printer-heading">
    <div class="oe-printer-identity">
      <div class="oe-printer-icon" aria-hidden="true">
        <svg
          viewBox="0 0 32 32"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          ><path
            d="M7 24V5h18v19M5 27h22M10 23h12M9 9h14M16 9v6m-3 0h6l-3 4-3-4Z"
          /></svg
        >
      </div>
      <div>
        <h3 id={`printer-${printer.id}`}>{printer.name}</h3>
        <span class={`oe-status oe-status-${statusTone}`}
          ><span class="oe-status-dot" aria-hidden="true"></span>{t(
            `state.${printer.state}`,
          )}</span
        >
      </div>
    </div>
    <Button
      variant="secondary"
      class="oe-secondary"
      aria-expanded={expanded}
      aria-controls={`settings-${printer.id}`}
      onclick={() => {
        expanded = !expanded;
      }}>{expanded ? t('printer.closeSettings') : t('printer.settings')}</Button
    >
  </div>
  <div class="oe-printer-body">
    {#if printer.warning === true || printer.pause_suggested === true}
      <div class="oe-notice" role="status">
        <h4>{t('printer.warningTitle')}</h4>
        <p>
          {printer.pause_suggested
            ? t('printer.pauseSuggested')
            : t('printer.warningHelp')}
        </p>
      </div>
    {/if}
    {#if printer.job_name}<p class="oe-job-name">{printer.job_name}</p>{/if}
    {#if printer.detail}<p class="oe-printer-detail">{printer.detail}</p>{/if}
    <dl class="oe-printer-facts">
      <div>
        <dt>{t('printer.monitoring')}</dt>
        <dd>{printer.enabled ? t('common.on') : t('common.off')}</dd>
      </div>
      <div>
        <dt>{t('printer.lastCheck')}</dt>
        <dd>
          {printer.last_inspection_at
            ? inspectionTime(printer.last_inspection_at)
            : t('printer.notYet')}
        </dd>
      </div>
      <div>
        <dt>{t('printer.quality')}</dt>
        <dd>
          {printer.quality === undefined
            ? t('common.unavailable')
            : t('printer.qualityValue', { value: printer.quality.toFixed(1) })}
        </dd>
      </div>
      <div>
        <dt>{t('printer.autoPause')}</dt>
        <dd>{printer.auto_pause ? t('common.on') : t('common.off')}</dd>
      </div>
    </dl>
    {#if printer.state === 'waiting_first_layer'}
      <div class="oe-first-layer">
        <div>
          <h4>{t('printer.firstLayerTitle')}</h4>
          <p>{t('printer.firstLayerHelp')}</p>
        </div>
      </div>
    {/if}
    {#if printer.state === 'quota'}<p class="oe-notice">
        {t('printer.quotaHelp')}
        <a
          class="oe-text-link"
          href="https://octoeverywhere.com/gadgetapi"
          target="_blank"
          rel="noopener noreferrer">{t('account.manageAccount')}</a
        >
      </p>{/if}
    {#if fault}<FaultAlert {fault} />{/if}
    {#if notice}<p class="oe-success" role="status">{notice}</p>{/if}
  </div>
  {#if expanded}
    <form
      id={`settings-${printer.id}`}
      class="oe-printer-settings"
      onsubmit={(event) => {
        event.preventDefault();
        void run('save');
      }}
    >
      {#if !printer.can_manage}<p class="oe-notice">
          {t('printer.readOnly')}
        </p>{/if}
      <div class="oe-check-row">
        <Checkbox
          id={`enabled-${printer.id}`}
          checked={settings.enabled}
          onCheckedChange={(enabled: boolean) => change({ enabled })}
          disabled={!printer.can_manage || !ready || busy !== undefined}
          aria-describedby={`enabled-help-${printer.id}`}
        />
        <div class="oe-check-copy">
          <Label for={`enabled-${printer.id}`} variant="option"
            ><strong>{t('printer.enable')}</strong></Label
          >
          <p class="oe-field-hint" id={`enabled-help-${printer.id}`}>
            {t('printer.enableHelp')}
          </p>
        </div>
      </div>
      {#if !ready}<p class="oe-field-hint">{t('printer.setupFirst')}</p>{/if}
      <div class="oe-camera-field">
        <Label for={`camera-${printer.id}`}>{t('printer.camera')}</Label>
        <div class="oe-camera-controls">
          <Select
            value={settings.camera_id}
            items={cameraItems}
            onValueChange={(camera_id: string) => change({ camera_id })}
            disabled={!printer.can_manage ||
              cameraItems.length === 0 ||
              busy !== undefined}
            ><SelectTrigger id={`camera-${printer.id}`} class="oe-input"
              ><SelectValue
                placeholder={t('printer.chooseCamera')}
              /></SelectTrigger
            ><SelectContent
              >{#each cameraItems as camera (camera.value)}<SelectItem
                  value={camera.value}
                  label={camera.label}>{camera.label}</SelectItem
                >{/each}</SelectContent
            ></Select
          >{#if printer.can_manage}<Button
              variant="secondary"
              class="oe-secondary"
              pending={busy === 'test'}
              disabled={!printer.camera_id ||
                draft !== undefined ||
                busy !== undefined}
              onclick={() => void run('test')}>{t('printer.testCamera')}</Button
            >{/if}
        </div>
        <p class="oe-field-hint">
          {cameraItems.length
            ? t('printer.cameraHelp')
            : t('printer.noCameraHelp')}
        </p>
        {#if draft}<p class="oe-field-hint">
            {t('printer.saveBeforeTest')}
          </p>{/if}
      </div>
      <div class="oe-check-row">
        <Checkbox
          id={`pause-${printer.id}`}
          checked={settings.auto_pause}
          onCheckedChange={(auto_pause: boolean) => change({ auto_pause })}
          disabled={!printer.can_manage || busy !== undefined}
          aria-describedby={`pause-help-${printer.id}`}
        />
        <div class="oe-check-copy">
          <Label for={`pause-${printer.id}`} variant="option"
            ><strong>{t('printer.autoPause')}</strong></Label
          >
          <p class="oe-field-hint" id={`pause-help-${printer.id}`}>
            {t('printer.autoPauseHelp')}
          </p>
          <a
            class="oe-text-link oe-permissions-link"
            href="https://github.com/OctoEverywhere/connect-extension#permit-automatic-pause-for-a-printer"
            target="_blank"
            rel="noopener noreferrer">{t('printer.autoPausePermissions')}</a
          >
        </div>
      </div>
      <div class="oe-confidence-fields">
        <div class="oe-form">
          <Label for={`warning-${printer.id}`}
            >{t('printer.warningConfidence')}</Label
          ><Input
            id={`warning-${printer.id}`}
            type="number"
            min="1"
            max="5"
            step="1"
            value={settings.warning_confidence}
            oninput={(event: Event & { currentTarget: HTMLInputElement }) =>
              change({ warning_confidence: event.currentTarget.valueAsNumber })}
            disabled={!printer.can_manage || busy !== undefined}
            class="oe-input"
            required
          />
        </div>
        <div class="oe-form">
          <Label for={`pause-confidence-${printer.id}`}
            >{t('printer.pauseConfidence')}</Label
          ><Input
            id={`pause-confidence-${printer.id}`}
            type="number"
            min="1"
            max="5"
            step="1"
            value={settings.pause_confidence}
            oninput={(event: Event & { currentTarget: HTMLInputElement }) =>
              change({ pause_confidence: event.currentTarget.valueAsNumber })}
            disabled={!printer.can_manage || busy !== undefined}
            class="oe-input"
            required
          />
        </div>
      </div>
      <p class="oe-field-hint">{t('printer.confidenceHelp')}</p>
      <div class="oe-form oe-warmup-field">
        <Label for={`warmup-${printer.id}`}>{t('printer.warmupLabel')}</Label
        ><Input
          id={`warmup-${printer.id}`}
          type="number"
          min="60"
          max="1800"
          step="1"
          value={settings.warmup_seconds}
          oninput={(event: Event & { currentTarget: HTMLInputElement }) =>
            change({ warmup_seconds: event.currentTarget.valueAsNumber })}
          disabled={!printer.can_manage || busy !== undefined}
          class="oe-input"
          required
          aria-describedby={`warmup-help-${printer.id}`}
        />
        <p class="oe-field-hint" id={`warmup-help-${printer.id}`}>
          {t('printer.warmupHelp')}
        </p>
      </div>
      {#if printer.can_manage}<div class="oe-actions">
          <Button
            type="submit"
            pending={busy === 'save'}
            disabled={draft === undefined ||
              busy !== undefined ||
              (settings.enabled && !settings.camera_id)}
            class="oe-primary">{t('printer.save')}</Button
          >{#if draft}<Button
              variant="ghost"
              disabled={busy !== undefined}
              onclick={() => {
                draft = undefined;
              }}>{t('common.cancel')}</Button
            >{/if}
        </div>{/if}
    </form>
  {/if}
</article>
