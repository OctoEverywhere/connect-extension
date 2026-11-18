<script lang="ts">
  import { describeFault, type FaultText } from '@prusa/connect-ui/i18n';
  import {
    Button,
    FaultAlert,
    Input,
    Label,
  } from '@prusa/connect-ui/primitives';

  import { apiOn, type Status } from './api';
  import { heldHost } from './host';

  let {
    status,
    onrefresh,
  }: { status: Status; onrefresh: () => Promise<void> } = $props();
  const host = heldHost();
  const t = host.t;
  const api = apiOn(host);
  let key = $state('');
  let url = $state('');
  let token = $state('');
  let keyVisible = $state(false);
  let editingKey = $state(false);
  let editingConnect = $state(false);
  let busy = $state<'key' | 'connect' | undefined>();
  let fault = $state<FaultText | undefined>();
  let saved = $state('');

  $effect(() => {
    if (!url && status.connect_url) url = status.connect_url;
  });

  async function saveKey(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !key.trim()) return;
    busy = 'key';
    fault = undefined;
    saved = '';
    try {
      await api.saveKey(key.trim(), host.signal);
      key = '';
      keyVisible = false;
      editingKey = false;
      await onrefresh();
      saved = t('account.keySaved');
    } catch (thrown: unknown) {
      if (!host.signal.aborted) fault = describeFault(thrown);
    } finally {
      busy = undefined;
    }
  }

  async function saveConnect(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !url.trim() || !token.trim()) return;
    busy = 'connect';
    fault = undefined;
    saved = '';
    try {
      await api.saveConnect(url.trim(), token.trim(), host.signal);
      token = '';
      editingConnect = false;
      await onrefresh();
      saved = t('account.connectSaved');
    } catch (thrown: unknown) {
      if (!host.signal.aborted) fault = describeFault(thrown);
    } finally {
      busy = undefined;
    }
  }
</script>

<section class="oe-card oe-setup" aria-labelledby="oe-setup-title">
  <div class="oe-card-heading">
    <div>
      <h2 id="oe-setup-title">{t('account.title')}</h2>
      <p>{t('account.description')}</p>
    </div>
    {#if status.configured && status.camera_configured}
      <span class="oe-status oe-status-good">{t('account.connected')}</span>
    {/if}
  </div>

  {#if !status.can_manage}
    <p class="oe-notice">{t('account.readOnly')}</p>
  {/if}
  {#if fault}<FaultAlert {fault} />{/if}
  {#if saved}<p class="oe-success" role="status">{saved}</p>{/if}

  <div class="oe-setup-step">
    <span class="oe-step-number" aria-hidden="true">{t('account.stepOne')}</span
    >
    <div class="oe-step-body">
      <div class="oe-row">
        <h3>{t('account.gadgetTitle')}</h3>
        {#if status.configured && !editingKey}
          <span class="oe-status oe-status-good">{t('account.keyReady')}</span>
        {/if}
      </div>
      {#if status.configured && !editingKey}
        <p>{t('account.keyStored')}</p>
        <div class="oe-actions">
          <a
            class="oe-text-link"
            href="https://octoeverywhere.com/gadgetapi"
            target="_blank"
            rel="noopener noreferrer">{t('account.manageAccount')}</a
          >
          {#if status.can_manage}<Button
              variant="secondary"
              class="oe-secondary"
              onclick={() => {
                editingKey = true;
                saved = '';
              }}>{t('account.replaceKey')}</Button
            >{/if}
        </div>
      {:else if status.can_manage}
        <p>
          {t('account.keyHelp')}
          <a
            class="oe-text-link"
            href="https://octoeverywhere.com/gadgetapi"
            target="_blank"
            rel="noopener noreferrer">{t('account.getKey')}</a
          >
        </p>
        <form onsubmit={(event) => void saveKey(event)} class="oe-form">
          <Label for="oe-api-key">{t('account.keyLabel')}</Label>
          <div class="oe-secret-field">
            <Input
              id="oe-api-key"
              type={keyVisible ? 'text' : 'password'}
              value={key}
              oninput={(event: Event & { currentTarget: HTMLInputElement }) => {
                key = event.currentTarget.value;
              }}
              autocomplete="off"
              spellcheck={false}
              required
              class="oe-input"
              aria-describedby="oe-key-hint"
            />
            <Button
              variant="secondary"
              class="oe-secondary"
              aria-controls="oe-api-key"
              aria-pressed={keyVisible}
              onclick={() => {
                keyVisible = !keyVisible;
              }}>{keyVisible ? t('common.hide') : t('common.show')}</Button
            >
          </div>
          <p class="oe-field-hint" id="oe-key-hint">
            {t('account.keyPrivacy')}
          </p>
          <div class="oe-actions">
            <Button
              type="submit"
              pending={busy === 'key'}
              disabled={!key.trim() || busy !== undefined}
              class="oe-primary">{t('account.validateKey')}</Button
            >
            {#if status.configured}<Button
                variant="ghost"
                onclick={() => {
                  editingKey = false;
                  key = '';
                }}>{t('common.cancel')}</Button
              >{/if}
          </div>
        </form>
      {:else}
        <p>{t('account.keyMissing')}</p>
      {/if}
    </div>
  </div>

  <div class="oe-setup-step">
    <span class="oe-step-number" aria-hidden="true">{t('account.stepTwo')}</span
    >
    <div class="oe-step-body">
      <div class="oe-row">
        <h3>{t('account.cameraTitle')}</h3>
        {#if status.camera_configured && !editingConnect}<span
            class="oe-status oe-status-good">{t('account.connected')}</span
          >{/if}
      </div>
      {#if status.camera_configured && !editingConnect}
        <p>{t('account.cameraReady')}</p>
        <p class="oe-url">{status.connect_url}</p>
        {#if status.can_manage}<Button
            variant="secondary"
            class="oe-secondary"
            onclick={() => {
              editingConnect = true;
              saved = '';
            }}>{t('account.editConnection')}</Button
          >{/if}
      {:else if status.can_manage && status.configured}
        <p>{t('account.cameraHelp')}</p>
        <p class="oe-field-hint">{t('account.tokenHelp')}</p>
        <form onsubmit={(event) => void saveConnect(event)} class="oe-form">
          <Label for="oe-connect-url">{t('account.urlLabel')}</Label>
          <Input
            id="oe-connect-url"
            type="url"
            value={url}
            readonly
            required
            class="oe-input"
            aria-describedby="oe-url-hint"
          />
          <p class="oe-field-hint" id="oe-url-hint">{t('account.urlHelp')}</p>
          <Label for="oe-connect-token">{t('account.tokenLabel')}</Label>
          <Input
            id="oe-connect-token"
            type="password"
            value={token}
            oninput={(event: Event & { currentTarget: HTMLInputElement }) => {
              token = event.currentTarget.value;
            }}
            autocomplete="off"
            spellcheck={false}
            required
            class="oe-input"
          />
          <div class="oe-actions">
            <Button
              type="submit"
              pending={busy === 'connect'}
              disabled={!url.trim() || !token.trim() || busy !== undefined}
              class="oe-primary">{t('account.validateConnection')}</Button
            >
            {#if status.camera_configured}<Button
                variant="ghost"
                onclick={() => {
                  editingConnect = false;
                  token = '';
                }}>{t('common.cancel')}</Button
              >{/if}
          </div>
        </form>
      {:else}
        <p>{t('account.cameraNext')}</p>
      {/if}
    </div>
  </div>
</section>
