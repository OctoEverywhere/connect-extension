<script lang="ts">
  import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';
  import { Button, FaultAlert, Skeleton } from '@prusa/connect-ui/primitives';
  import { onMount } from 'svelte';
  import AccountSettings from './account-settings.svelte';
  import { apiOn, type Status } from './api';
  import gadgetImage from './assets/gadget.svg';
  import oeLogo from './assets/octoeverywhere.svg';
  import { heldHost } from './host';
  import PrinterCard from './printer-card.svelte';

  const host = heldHost();
  const t = host.t;
  const api = apiOn(host);
  const lifetime = new AbortController();
  let status = $state<Status | undefined>();
  let fault = $state<FaultText | undefined>();
  let refreshing = $state(false);
  let showAccount = $state(false);
  let historyLimit = $state(10);
  const ready = $derived(
    Boolean(status?.configured && status.camera_configured),
  );
  const showSetup = $derived(!ready || showAccount);
  const activeCount = $derived(
    status?.printers.filter((printer) => printer.enabled).length ?? 0,
  );

  const isStopped = () => lifetime.signal.aborted || host.signal.aborted;

  async function refresh() {
    if (refreshing || isStopped()) return;
    refreshing = true;
    try {
      status = await api.status(lifetime.signal);
      fault = undefined;
    } catch (thrown: unknown) {
      if (!isStopped()) fault = describeFault(thrown);
    } finally {
      refreshing = false;
    }
  }

  onMount(() => {
    void refresh();
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') void refresh();
    }, 15000);
    const stop = () => {
      clearInterval(timer);
      lifetime.abort();
    };
    host.signal.addEventListener('abort', stop, { once: true });
    return () => {
      stop();
      host.signal.removeEventListener('abort', stop);
    };
  });

  function formatTime(value: string) {
    const date = new Date(value);
    return Number.isNaN(date.getTime())
      ? t('common.unavailable')
      : new Intl.DateTimeFormat(undefined, {
          month: 'short',
          day: 'numeric',
          hour: 'numeric',
          minute: '2-digit',
        }).format(date);
  }
</script>

<div class="oe-extension">
  <header class="oe-header">
    <div class="oe-brand">
      <img src={oeLogo} alt="" width="38" height="38" /><span
        >{t('brand.name')}</span
      >
    </div>
    <a
      class="oe-help-link"
      href="https://docs.octoeverywhere.com/ai-failure-detection-apis/overview/"
      target="_blank"
      rel="noopener noreferrer">{t('common.help')}</a
    >
  </header>
  <div class="oe-content">
    <div class="oe-page-heading">
      <img src={gadgetImage} alt="" width="72" height="72" class="oe-gadget" />
      <div>
        <h1>{t('page.title')}</h1>
        <p>{t('page.description')}</p>
      </div>
    </div>
    {#if fault}<div class="oe-page-error">
        <FaultAlert {fault} /><Button
          variant="secondary"
          class="oe-secondary"
          pending={refreshing}
          onclick={() => void refresh()}>{t('common.retry')}</Button
        >
      </div>{/if}
    {#if !status && !fault}<div
        class="oe-loading"
        aria-busy="true"
        aria-label={t('page.loading')}
      >
        <Skeleton class="h-48 w-full" /><Skeleton class="h-32 w-full" />
      </div>{/if}
    {#if status}
      {#if status.error}<p class="oe-notice">{status.error}</p>{/if}
      {#if showSetup}
        <div class="oe-setup-layout">
          <AccountSettings {status} onrefresh={refresh} />
          <aside class="oe-setup-aside">
            <h2>{t('setup.howTitle')}</h2>
            <p>{t('setup.howBody')}</p>
            <div class="oe-aside-section">
              <h3>{t('setup.controlTitle')}</h3>
              <p>{t('setup.controlBody')}</p>
            </div>
            <div class="oe-aside-section">
              <h3>{t('setup.privacyTitle')}</h3>
              <p>{t('setup.privacyBody')}</p>
              <a
                class="oe-text-link"
                href="https://octoeverywhere.com/privacy"
                target="_blank"
                rel="noopener noreferrer">{t('setup.privacyLink')}</a
              >
            </div>
          </aside>
        </div>
      {:else}
        <div class="oe-card oe-account-summary">
          <div>
            <span class="oe-status oe-status-good"
              ><span class="oe-status-dot" aria-hidden="true"></span>{t(
                'account.ready',
              )}</span
            >
            <p>{t('account.summary', { count: activeCount })}</p>
          </div>
          <Button
            variant="secondary"
            class="oe-secondary"
            onclick={() => {
              showAccount = true;
            }}>{t('account.settings')}</Button
          >
        </div>
      {/if}
      {#if showAccount && ready}<Button
          variant="ghost"
          class="oe-close-account"
          onclick={() => {
            showAccount = false;
          }}>{t('account.close')}</Button
        >{/if}
      <section class="oe-printers-section" aria-labelledby="oe-printers-title">
        <div class="oe-section-heading">
          <div>
            <h2 id="oe-printers-title">{t('printers.title')}</h2>
            <p>{t('printers.description')}</p>
          </div>
          <Button
            variant="secondary"
            class="oe-secondary"
            pending={refreshing}
            onclick={() => void refresh()}>{t('common.refresh')}</Button
          >
        </div>
        {#if status.printers.length}<div class="oe-printer-list">
            {#each status.printers as printer (printer.id)}<PrinterCard
                {printer}
                {ready}
                onrefresh={refresh}
              />{/each}
          </div>{:else}<div class="oe-card oe-empty">
            <h3>{t('printers.emptyTitle')}</h3>
            <p>{t('printers.emptyBody')}</p>
          </div>{/if}
      </section>
      <section class="oe-card oe-history" aria-labelledby="oe-history-title">
        <div class="oe-card-heading">
          <div>
            <h2 id="oe-history-title">{t('history.title')}</h2>
            <p>{t('history.description')}</p>
          </div>
        </div>
        {#if status.history.length}
          <ol class="oe-history-list">
            {#each status.history.slice(0, historyLimit) as entry, index (`${entry.at}-${entry.printer_id}-${String(index)}`)}<li
                class="oe-history-entry"
              >
                <span
                  class={`oe-history-mark oe-history-${entry.kind}`}
                  aria-hidden="true"
                ></span>
                <div>
                  <div class="oe-history-top">
                    <strong>{entry.printer_name}</strong><time
                      datetime={entry.at}>{formatTime(entry.at)}</time
                    >
                  </div>
                  <p>{entry.message}</p>
                  {#if entry.job_name}<span class="oe-field-hint"
                      >{entry.job_name}</span
                    >{/if}
                </div>
              </li>{/each}
          </ol>
          {#if status.history.length > historyLimit}<Button
              variant="secondary"
              class="oe-secondary"
              onclick={() => {
                historyLimit += 20;
              }}>{t('history.showMore')}</Button
            >{/if}
        {:else}<div class="oe-history-empty">
            <p>{t('history.empty')}</p>
            <span>{t('history.emptyHelp')}</span>
          </div>{/if}
      </section>
    {/if}
    <footer class="oe-footer">
      <span>{t('footer.text')}</span><a
        href="https://octoeverywhere.com/gadgetapi"
        target="_blank"
        rel="noopener noreferrer">{t('footer.account')}</a
      >
    </footer>
  </div>
</div>
