<script setup lang="ts">
import { ref, watch } from "vue";
import { SettingsService } from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import { handleResult, refreshSettings, setTheme, settings, theme, toast, type Theme } from "../store";
import Icon from "./Icon.vue";
import Modal from "./Modal.vue";
import UiCheckbox from "./UiCheckbox.vue";
import UiInput from "./UiInput.vue";
import UiSlider from "./UiSlider.vue";
import UiSelect from "./UiSelect.vue";
import IpServiceRow from "./IpServiceRow.vue";

const binary = ref("");
const binaryError = ref("");
const pendingDir = ref("");
const reconnect = ref({ enabled: true, attempts: 3, interval: 5 });
async function saveReconnect() {
  const r = reconnect.value;
  handleResult(await SettingsService.SetAutoReconnect(r.enabled, r.attempts, r.interval), "Could not save");
  await refreshSettings();
}
const winW = ref("");
const winH = ref("");
const winError = ref("");
const presets = [
  { label: "Phone", w: 440, h: 820 },
  { label: "Tablet", w: 760, h: 900 },
  { label: "Desktop", w: 1120, h: 780 },
];
async function applyWindow(w = Number(winW.value), h = Number(winH.value)) {
  const r = await SettingsService.SetWindowSize(Math.round(w), Math.round(h));
  winError.value = r.ok ? "" : r.message;
  if (r.ok) toast("success", "Window size saved", `${Math.round(w)} × ${Math.round(h)}`);
  await refreshSettings();
}
async function useCurrent() {
  const r = await SettingsService.UseCurrentWindowSize();
  winError.value = r.ok ? "" : r.message;
  if (r.ok) toast("success", "Current window size saved");
  await refreshSettings();
}
const services = ref<{ name: string; url: string; enabled: boolean }[]>([]);
const servicesError = ref("");
const servicesDirty = ref(false);

watch(settings, (s) => {
  if (!s) return;
  binary.value = s.openvpnBinary;
  reconnect.value = { enabled: s.autoReconnect, attempts: s.reconnectAttempts, interval: s.reconnectInterval };
  winW.value = String(s.windowWidth);
  winH.value = String(s.windowHeight);
  if (!servicesDirty.value) services.value = (s.ipServices ?? []).map((x) => ({ ...x }));
}, { immediate: true });

function addService() {
  services.value.push({ name: "", url: "https://", enabled: true });
  servicesDirty.value = true;
}
function editService(i: number, field: "name" | "url", value: string) {
  services.value[i][field] = value;
  servicesDirty.value = true;
}
function toggleService(i: number, on: boolean) {
  services.value[i].enabled = on;
  servicesDirty.value = true;
}
function removeService(i: number) {
  services.value.splice(i, 1);
  servicesDirty.value = true;
}
async function saveServices() {
  const r = await SettingsService.SetIPServices(services.value);
  servicesError.value = r.ok ? "" : r.message;
  if (r.ok) {
    servicesDirty.value = false;
    toast("success", "Public IP services saved");
  }
  await refreshSettings();
}
async function resetServices() {
  servicesDirty.value = false;
  servicesError.value = "";
  if (handleResult(await SettingsService.ResetIPServices(), "Could not reset")) await refreshSettings();
}

async function saveBinary() {
  const r = await SettingsService.SetOpenVPNBinary(binary.value);
  binaryError.value = r.ok ? "" : r.message;
  if (r.ok) toast("success", binary.value ? "OpenVPN path saved" : "Using auto-detected OpenVPN");
  await refreshSettings();
}
async function browseBinary() {
  const p = await SettingsService.BrowseOpenVPNBinary();
  if (p) { binary.value = p; await saveBinary(); }
}
async function setVerbosity(v: number) {
  handleResult(await SettingsService.SetLogVerbosity(v), "Could not save");
  await refreshSettings();
}
async function setPublicIp(on: boolean) {
  handleResult(await SettingsService.SetPublicIPCheck(on), "Could not save");
  await refreshSettings();
}
async function chooseDir() {
  const p = await SettingsService.BrowseProfilesDir();
  if (p) pendingDir.value = p;
}
async function applyDir(copy: boolean) {
  const dir = pendingDir.value;
  pendingDir.value = "";
  if (handleResult(await SettingsService.SetProfilesDir(dir, copy), "Could not change the folder")) {
    toast("success", "Profiles folder changed", dir);
    await refreshSettings();
  }
}
const themes: { v: Theme; label: string }[] = [
  { v: "light", label: "Light" },
  { v: "dark", label: "Dark" },
  { v: "system", label: "Match system" },
];
</script>

<template>
  <div v-if="settings" class="flex flex-col gap-4">
    <h1 class="text-[26px] font-semibold tracking-tight">Settings</h1>

    <!-- One surface; every row is "what it does" on the left and its control on the right. -->
    <div class="divide-y divide-line overflow-hidden rounded-xl border border-line bg-surface shadow-card">
      <h2 class="bg-surface2 px-4 py-2 text-[12px] font-semibold uppercase tracking-wide text-muted sm:px-5">Appearance</h2>

      <div class="row">
        <div class="row-text"><p class="row-title">Theme</p><p class="row-hint">Light, dark, or follow the system.</p></div>
        <div class="row-control" role="radiogroup" aria-label="Theme">
          <button v-for="t in themes" :key="t.v" role="radio" :aria-checked="theme === t.v" class="h-10 rounded-lg border px-4 text-[14px]" :class="theme === t.v ? 'border-accent bg-accent-soft font-medium text-accent-text' : 'border-line hover:bg-surface2'" @click="setTheme(t.v)">{{ t.label }}</button>
        </div>
      </div>

      <div class="row">
        <div class="row-text"><p class="row-title">Window size</p><p class="row-hint">Size the window opens with. A tall, narrow window works like a phone screen. Applied now and at every start.</p></div>
        <div class="row-control flex-col !items-stretch">
          <div class="flex flex-wrap gap-2">
            <button
              v-for="p in presets"
              :key="p.label"
              class="h-10 rounded-lg border px-3 text-[14px]"
              :class="Number(winW) === p.w && Number(winH) === p.h ? 'border-accent bg-accent-soft font-medium text-accent-text' : 'border-line hover:bg-surface2'"
              @click="winW = String(p.w); winH = String(p.h); applyWindow(p.w, p.h)"
            >{{ p.label }} <span class="text-muted">{{ p.w }}×{{ p.h }}</span></button>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <UiInput v-model="winW" class="!w-24" label="Window width" type="number" placeholder="Width" @enter="applyWindow()" />
            <span class="text-muted">×</span>
            <UiInput v-model="winH" class="!w-24" label="Window height" type="number" placeholder="Height" @enter="applyWindow()" />
            <button class="h-10 rounded-lg bg-accent px-4 text-[14px] font-medium text-white hover:opacity-90" @click="applyWindow()">Apply</button>
            <button class="h-10 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="useCurrent">Use current size</button>
          </div>
          <p v-if="winError" class="text-[13px] text-danger" role="alert">{{ winError }}</p>
        </div>
      </div>

      <h2 class="bg-surface2 px-4 py-2 text-[12px] font-semibold uppercase tracking-wide text-muted sm:px-5">OpenVPN</h2>

      <div class="row">
        <div class="row-text">
          <p class="row-title">Executable</p>
          <p class="row-hint">Detected: <span class="font-mono">{{ settings.detectedBinary || "not found" }}</span>. Leave empty to use it.</p>
        </div>
        <div class="row-control flex-col !items-stretch">
          <div class="flex gap-2">
            <UiInput v-model="binary" class="flex-1" mono label="OpenVPN executable path" placeholder="/usr/sbin/openvpn" :invalid="!!binaryError" @enter="saveBinary" />
            <button class="h-10 shrink-0 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="browseBinary">Browse…</button>
            <button class="h-10 shrink-0 rounded-lg bg-accent px-5 text-[14px] font-medium text-white hover:opacity-90" @click="saveBinary">Save</button>
          </div>
          <p v-if="binaryError" class="text-[13px] text-danger" role="alert">{{ binaryError }}</p>
        </div>
      </div>

      <div class="row">
        <div class="row-text"><p class="row-title">Log verbosity</p><p class="row-hint">0 quiet – 9 very detailed. Applies to the next connection.</p></div>
        <div class="row-control"><UiSlider :model-value="settings.logVerbosity" :min="0" :max="9" label="Log verbosity" @change="setVerbosity" /></div>
      </div>

      <div class="row">
        <div class="row-text">
          <p class="row-title">Auto reconnect</p>
          <p class="row-hint">OpenVPN already re-establishes dropped sessions by itself. If it exits unexpectedly, the app starts it again (you may be asked to authorize). Wrong credentials are never retried. Applies to the next connection.</p>
        </div>
        <div class="row-control">
          <UiCheckbox v-model="reconnect.enabled" label="Enabled" @update:model-value="saveReconnect" />
          <template v-if="reconnect.enabled">
            <span class="text-[13px] text-muted">Attempts</span>
            <UiSlider :model-value="reconnect.attempts" :min="1" :max="10" label="Reconnect attempts" @change="(v: number) => { reconnect.attempts = v; saveReconnect(); }" />
            <UiSelect
              v-model="reconnect.interval"
              label="Wait between attempts"
              width="9rem"
              :options="[{ value: 3, label: '3 seconds' }, { value: 5, label: '5 seconds' }, { value: 10, label: '10 seconds' }, { value: 30, label: '30 seconds' }, { value: 60, label: '1 minute' }]"
              @update:model-value="saveReconnect"
            />
          </template>
        </div>
      </div>

      <div class="row">
        <div class="row-text"><p class="row-title">Administrator access</p><p class="row-hint">You authorize each time OpenVPN starts; the app itself never runs as administrator.</p></div>
        <div class="row-control"><span class="rounded-md bg-surface2 px-3 py-2 text-[14px] font-medium">{{ settings.privilege }}</span></div>
      </div>

      <h2 class="bg-surface2 px-4 py-2 text-[12px] font-semibold uppercase tracking-wide text-muted sm:px-5">Profiles</h2>

      <div class="row">
        <div class="row-text"><p class="row-title">Profiles folder</p><p class="row-hint">Imported profiles and their certificates are stored here. Your original files are never modified.</p></div>
        <div class="row-control flex-col !items-stretch">
          <p class="selectable break-all rounded-lg bg-surface2 px-3 py-2 font-mono text-[13px]">{{ settings.profilesDir }}</p>
          <div class="flex flex-wrap gap-2">
            <button class="flex h-10 items-center gap-2 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="SettingsService.OpenProfilesDir()"><Icon name="external" :size="15" />Open folder</button>
            <button class="flex h-10 items-center gap-2 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="chooseDir"><Icon name="folder" :size="15" />Change…</button>
          </div>
        </div>
      </div>

      <h2 class="bg-surface2 px-4 py-2 text-[12px] font-semibold uppercase tracking-wide text-muted sm:px-5">Public IP</h2>

      <div class="row">
        <div class="row-text"><p class="row-title">Look up after connecting</p><p class="row-hint">Asks the services below for your address through the tunnel. The result is shown only when they agree.</p></div>
        <div class="row-control"><UiCheckbox :model-value="settings.publicIpCheck" label="Enabled" @update:model-value="setPublicIp" /></div>
      </div>

      <div class="px-4 py-4 sm:px-5">
        <p class="row-title mb-3">Services</p>
        <ul class="flex flex-col gap-2">
          <IpServiceRow
            v-for="(svc, idx) in services"
            :key="idx"
            :name="svc.name"
            :url="svc.url"
            :enabled="svc.enabled"
            @name="editService(idx, 'name', $event)"
            @url="editService(idx, 'url', $event)"
            @enabled="toggleService(idx, $event)"
            @remove="removeService(idx)"
          />
        </ul>
        <p v-if="!services.length" class="text-[13px] text-muted">No services configured; the public IP will not be looked up.</p>
        <p v-if="servicesError" class="mt-2 text-[13px] text-danger" role="alert">{{ servicesError }}</p>
        <div class="mt-4 flex flex-wrap gap-2">
          <button class="h-10 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="addService">+ Add service</button>
          <button class="h-10 rounded-lg border border-line px-3 text-[14px] hover:bg-surface2" @click="resetServices">Reset to defaults</button>
          <button class="h-10 rounded-lg bg-accent px-5 text-[14px] font-medium text-white hover:opacity-90 disabled:opacity-50" :disabled="!servicesDirty" @click="saveServices">Save services</button>
        </div>
      </div>
    </div>

    <Modal v-if="pendingDir" title="Change profiles folder" @close="pendingDir = ''">
      <p class="text-[14px]">Use <span class="selectable break-all font-mono">{{ pendingDir }}</span> for imported profiles?</p>
      <p class="mt-2 text-[13px] text-muted">You can copy your current profiles there. The old folder is left as it is.</p>
      <template #actions>
        <button class="rounded-lg border border-line px-3 py-2 text-[14px]" @click="pendingDir = ''">Cancel</button>
        <button class="rounded-lg border border-line px-3 py-2 text-[14px]" @click="applyDir(false)">Switch only</button>
        <button class="primary rounded-lg bg-accent px-3 py-2 text-[14px] font-medium text-white" @click="applyDir(true)">Copy profiles and switch</button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
/* Label/description on the left, control on the right; stacked on narrow windows. */
.row {
  display: grid;
  gap: 0.75rem 2rem;
  padding: 1rem;
  align-items: center;
}
@media (min-width: 640px) {
  .row { padding: 1rem 1.25rem; }
}
@media (min-width: 900px) {
  .row { grid-template-columns: minmax(14rem, 2fr) minmax(0, 3fr); }
  .row-control { justify-content: flex-end; }
}
.row-title { font-weight: 500; font-size: 14px; }
.row-hint { margin-top: 0.125rem; font-size: 13px; color: var(--muted); }
.row-control { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; min-width: 0; }
</style>
