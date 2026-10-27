<script setup lang="ts">
import { computed } from "vue";
import { conn, effectiveDark, menuOpen, setTheme, theme } from "../store";
import Icon from "./Icon.vue";

const mode = computed(() => (theme.value === "system" ? (effectiveDark.value ? "dark" : "light") : theme.value));
const status = computed(() => {
  switch (conn.value.state) {
    case "connected": return { dot: "bg-accent", label: "Connected" };
    case "connecting": return { dot: "bg-warn pulse", label: "Connecting…" };
    case "disconnecting": return { dot: "bg-warn pulse", label: "Disconnecting…" };
    case "error": return { dot: "bg-danger", label: "Failed" };
    case "unknown": return { dot: "bg-warn pulse", label: "Checking…" };
  }
  return conn.value.starting ? { dot: "bg-warn pulse", label: "Starting…" } : { dot: "bg-muted/60", label: "Disconnected" };
});
</script>

<template>
  <header class="relative z-30 flex h-14 shrink-0 items-center justify-between gap-2 border-b border-line bg-surface px-3 sm:px-5">
    <div class="flex min-w-0 items-center gap-2">
      <button
        class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg hover:bg-surface2"
        :aria-expanded="menuOpen"
        aria-controls="main-menu"
        aria-label="Menu"
        @click="menuOpen = !menuOpen"
      >
        <Icon :name="menuOpen ? 'x' : 'menu'" :size="22" />
      </button>
      <svg class="shrink-0" width="28" height="28" viewBox="0 0 32 32" aria-hidden="true">
        <circle cx="16" cy="16" r="12" fill="none" stroke="#f97316" stroke-width="4.5" />
        <path d="M16 10a3.2 3.2 0 011.6 6L18.6 23h-5.2l1-7A3.2 3.2 0 0116 10z" fill="#0f2a44" class="dark:fill-slate-100" />
      </svg>
      <span class="truncate text-[16px] font-semibold tracking-tight">OpenVPN Client</span>
    </div>

    <div class="flex shrink-0 items-center gap-2">
      <span class="flex items-center gap-2 rounded-full bg-surface2 px-2.5 py-1.5 text-[12px] font-medium" role="status" :title="status.label">
        <span class="h-2.5 w-2.5 rounded-full" :class="status.dot" aria-hidden="true" />
        <span class="hidden min-[520px]:inline">{{ status.label }}</span>
      </span>
      <div class="flex rounded-full border border-line bg-surface2 p-0.5" role="group" aria-label="Theme">
        <button
          v-for="o in [
            { v: 'light', label: 'Light', icon: 'sun' },
            { v: 'dark', label: 'Dark', icon: 'moon' },
          ]"
          :key="o.v"
          class="flex items-center gap-1.5 rounded-full px-2.5 py-1.5 text-[13px] font-medium transition-colors sm:px-3.5"
          :class="mode === o.v ? 'bg-fg text-bg' : 'text-muted hover:text-fg'"
          :aria-pressed="mode === o.v"
          :aria-label="o.label"
          :title="o.label"
          @click="setTheme(o.v as 'light' | 'dark')"
        >
          <Icon :name="o.icon" :size="16" /><span class="hidden sm:inline">{{ o.label }}</span>
        </button>
      </div>
    </div>
  </header>
</template>
