<script setup lang="ts">
import { computed, ref } from "vue";
import { ConnectionService } from "../../bindings/github.com/mrmmg/openvpn-wails/internal/backend";
import { conn, handleResult, now, toast, traffic } from "../store";
import { formatBytes, formatDuration, splitRate } from "../format";
import type { Profile } from "../../bindings/github.com/mrmmg/openvpn-wails/internal/backend";
import Icon from "./Icon.vue";
import TrafficChart from "./TrafficChart.vue";

const props = defineProps<{ profile: Profile }>();
const emit = defineEmits<{ (e: "retry"): void; (e: "prompt"): void }>();

const c = computed(() => conn.value);
const last = computed(() => traffic.value[traffic.value.length - 1]);
const down = computed(() => splitRate(last.value?.down ?? 0));
const up = computed(() => splitRate(last.value?.up ?? 0));
const connected = computed(() => c.value.state === "connected");
const uptime = computed(() => (connected.value && c.value.connectedSince ? formatDuration(now.value - c.value.connectedSince) : "—"));
const NA = "Not available";
const ROUTES_PREVIEW = 6;
const allRoutes = ref(false);
const shownRoutes = computed(() => (allRoutes.value ? c.value.routes ?? [] : (c.value.routes ?? []).slice(0, ROUTES_PREVIEW)));
const dash = (v: string) => v || NA;

const status = computed(() => {
  switch (c.value.state) {
    case "connected": return { label: "Connected", chip: "bg-accent-soft text-accent-text", dot: "bg-accent", icon: "check" };
    case "connecting": return { label: c.value.starting ? "Starting…" : "Connecting…", chip: "bg-warn-soft text-warn", dot: "bg-warn pulse", icon: "refresh" };
    case "disconnecting": return { label: "Disconnecting…", chip: "bg-warn-soft text-warn", dot: "bg-warn pulse", icon: "refresh" };
    case "error": return { label: "Error", chip: "bg-danger-soft text-danger", dot: "bg-danger", icon: "alert" };
    case "unknown": return { label: "Checking…", chip: "bg-warn-soft text-warn", dot: "bg-warn pulse", icon: "refresh" };
  }
  return { label: "Disconnected", chip: "bg-surface2 text-muted", dot: "bg-muted", icon: "minus" };
});

const server = computed(() => {
  const host = c.value.serverHost || props.profile.server.split(":")[0];
  const port = c.value.serverPort || props.profile.server.split(":")[1] || "";
  return host ? (port ? `${host}:${port}` : host) : NA;
});

const publicIp = computed(() => {
  switch (c.value.publicIpState) {
    case "ok": return c.value.publicIp;
    case "checking": return "Checking…";
    case "failed": return "Could not be determined";
    case "off": return "Lookup disabled in Settings";
  }
  return NA;
});

const canStop = computed(() => !c.value.starting && c.value.state !== "disconnecting" && c.value.state !== "disconnected" && c.value.state !== "error");

async function disconnect() {
  handleResult(await ConnectionService.Disconnect(), "Could not disconnect");
}
async function force() {
  if (handleResult(await ConnectionService.ForceDisconnect(), "Force stop failed")) toast("info", "OpenVPN was stopped");
}
</script>

<template>
  <div class="selectable border-t border-line px-3 pb-4 pt-4 sm:px-5">
    <!-- Status row -->
    <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
      <div class="min-w-[130px]">
        <p class="text-[13px] text-muted">Status</p>
        <span class="mt-1 inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-[14px] font-medium" :class="status.chip" role="status">
          <Icon :name="status.icon" :size="15" />{{ status.label }}
        </span>
      </div>
      <div class="hidden h-12 w-px bg-line sm:block" />
      <div class="min-w-[110px]">
        <p class="text-[13px] text-muted">VPN IP</p>
        <p class="mt-1 font-mono text-[20px] font-semibold" :class="c.vpnIp ? 'text-accent-text' : 'text-muted !text-sm !font-normal'">{{ c.vpnIp || NA }}</p>
      </div>
      <div class="hidden h-12 w-px bg-line sm:block" />
      <div class="min-w-[100px]">
        <p class="text-[13px] text-muted">Virtual Interface</p>
        <p class="mt-1 font-mono text-[20px] font-semibold" :class="c.interface ? '' : 'text-muted !text-sm !font-normal'">{{ c.interface || NA }}</p>
      </div>
      <div class="hidden h-12 w-px bg-line sm:block" />
      <div class="min-w-[100px]">
        <p class="text-[13px] text-muted">Uptime</p>
        <p class="mt-1 font-mono text-[20px] font-semibold tabular-nums" :class="connected ? '' : 'text-muted'">{{ uptime }}</p>
      </div>
      <div class="flex w-full gap-2 min-[520px]:ml-auto min-[520px]:w-auto">
        <button
          class="flex w-full items-center justify-center gap-2 rounded-lg bg-danger px-4 py-2.5 min-[520px]:w-auto text-[14px] font-medium text-white shadow-sm hover:opacity-90 disabled:opacity-50"
          :disabled="!canStop"
          @click="disconnect"
        >
          <Icon name="power" :size="16" />{{ c.state === "connecting" ? "Cancel" : "Disconnect" }}
        </button>
      </div>
    </div>

    <p v-if="c.detail && c.state === 'connecting'" class="mt-3 text-[13px] text-muted">{{ c.detail }}</p>
    <p v-if="c.message" class="mt-3 rounded-md bg-warn-soft px-3 py-2 text-[13px] text-warn">{{ c.message }}</p>

    <!-- Error -->
    <div v-if="c.error" class="mt-4 flex gap-3 rounded-lg bg-danger-soft p-3 text-danger" role="alert">
      <Icon name="alert" class="mt-0.5 shrink-0" />
      <div class="min-w-0 flex-1 text-[13px]">
        <p class="font-semibold">{{ c.error }}</p>
        <p v-if="c.errorHint" class="mt-0.5 opacity-90">{{ c.errorHint }}</p>
      </div>
      <div v-if="c.state === 'error' || c.state === 'disconnected'" class="flex shrink-0 gap-2 self-start">
        <button v-if="c.authFailed && profile.auth === 'file'" class="rounded-md bg-danger px-3 py-1 text-[13px] font-medium text-white" @click="emit('prompt')">Enter credentials</button>
        <button class="rounded-md border border-current px-3 py-1 text-[13px] font-medium" @click="emit('retry')">Retry</button>
      </div>
    </div>
    <div v-if="c.state === 'disconnecting'" class="mt-3 text-right">
      <button class="text-[13px] text-muted underline hover:text-fg" @click="force">Taking too long? Force stop</button>
    </div>

    <!-- Traffic -->
    <template v-if="connected">
      <div class="mt-4 border-t border-line pt-4">
        <p class="mb-3 text-[14px] font-medium">Traffic <span class="font-normal text-muted">(live)</span></p>
        <div class="flex flex-col gap-4 lg:flex-row">
          <div class="flex shrink-0 gap-6 lg:w-44 lg:flex-col lg:gap-5">
            <div class="flex items-center gap-3">
              <span class="flex h-11 w-11 items-center justify-center rounded-full border-2 border-down text-down-text"><Icon name="down" :size="22" /></span>
              <div>
                <p class="text-[12px] font-medium text-down-text">Download</p>
                <p class="text-[22px] font-semibold leading-tight tabular-nums text-down-text">{{ down[0] }} <span class="text-[13px] font-medium">{{ down[1] }}</span></p>
              </div>
            </div>
            <div class="flex items-center gap-3">
              <span class="flex h-11 w-11 items-center justify-center rounded-full border-2 border-up text-up-text"><Icon name="up" :size="22" /></span>
              <div>
                <p class="text-[12px] font-medium text-up-text">Upload</p>
                <p class="text-[22px] font-semibold leading-tight tabular-nums text-up-text">{{ up[0] }} <span class="text-[13px] font-medium">{{ up[1] }}</span></p>
              </div>
            </div>
          </div>
          <div class="min-w-0 flex-1">
            <div class="mb-1 flex justify-end gap-4 text-[12px] text-muted">
              <span class="flex items-center gap-1.5"><span class="h-0.5 w-4 bg-down" />Download</span>
              <span class="flex items-center gap-1.5"><span class="h-0.5 w-4 bg-up" />Upload</span>
            </div>
            <TrafficChart />
            <p v-if="!last" class="mt-1 text-center text-[12px] text-muted">Measuring… the first sample arrives after a second.</p>
          </div>
        </div>
      </div>

      <!-- Technical details -->
      <details class="group mt-4 border-t border-line pt-3" open>
        <summary class="flex cursor-pointer list-none items-center gap-2 text-[14px] font-medium">
          Technical details
          <Icon name="chevron" :size="14" class="rotate-90 transition-transform group-open:-rotate-90" />
        </summary>
        <dl class="mt-3 grid grid-cols-2 gap-x-6 gap-y-4 text-[13px] md:grid-cols-4">
          <div>
            <dt class="text-muted">Server</dt>
            <dd class="mt-1 break-all font-medium">{{ server }}</dd>
            <dd class="text-muted">{{ c.protocol || profile.protocol || "" }}</dd>
            <dd v-if="c.serverIp" class="font-mono text-[12px] text-muted">{{ c.serverIp }}</dd>
          </div>
          <div>
            <dt class="text-muted">Cipher</dt>
            <dd class="mt-1 font-medium">{{ dash(c.cipher) }}</dd>
          </div>
          <div>
            <dt class="text-muted">Auth</dt>
            <dd class="mt-1 font-medium">{{ dash(c.auth) }}</dd>
          </div>
          <div>
            <dt class="text-muted">DNS <span class="text-[11px]">(pushed)</span></dt>
            <dd v-if="c.dns?.length" class="mt-1 font-mono font-medium">
              <span v-for="d in c.dns ?? []" :key="d" class="block">{{ d }}</span>
            </dd>
            <dd v-else class="mt-1 text-muted">{{ NA }}</dd>
          </div>
          <div>
            <dt class="text-muted">Public IP</dt>
            <dd class="mt-1 font-mono font-medium" :class="c.publicIpState === 'ok' ? '' : '!font-sans !font-normal text-muted'">{{ publicIp }}</dd>
          </div>
          <div>
            <dt class="text-muted">Bytes In / Out</dt>
            <dd class="mt-1 font-medium tabular-nums text-down-text">{{ formatBytes(last?.totalDown ?? 0) }}</dd>
            <dd class="font-medium tabular-nums text-up-text">{{ formatBytes(last?.totalUp ?? 0) }}</dd>
          </div>
        </dl>

        <div class="mt-4">
          <div class="flex items-center justify-between text-[13px]">
            <p class="text-muted">Routes <span v-if="c.routes?.length">({{ c.routes.length }})</span></p>
            <button v-if="(c.routes?.length ?? 0) > ROUTES_PREVIEW" class="font-medium text-accent-text hover:underline" @click="allRoutes = !allRoutes">
              {{ allRoutes ? "Show fewer" : "Show all" }}
            </button>
          </div>
          <div
            v-if="c.routes?.length"
            class="mt-2 grid max-h-56 gap-1.5 overflow-auto rounded-lg bg-surface2 p-2 font-mono text-[12px] sm:grid-cols-2 xl:grid-cols-3"
            tabindex="0"
            aria-label="Routes installed for the tunnel"
          >
            <span v-for="r in shownRoutes" :key="r" class="truncate rounded bg-surface px-2 py-1" :title="r">{{ r }}</span>
          </div>
          <p v-else class="mt-1 text-[13px] text-muted">{{ NA }}</p>
        </div>
      </details>
    </template>
  </div>
</template>
