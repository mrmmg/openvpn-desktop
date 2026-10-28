<script setup lang="ts">
import { onMounted, ref } from "vue";
import { SettingsService } from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import type { AboutInfo } from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import Icon from "./Icon.vue";

const info = ref<AboutInfo | null>(null);
onMounted(async () => (info.value = await SettingsService.About()));
</script>

<template>
  <div class="selectable flex flex-col gap-4">
    <h1 class="text-[26px] font-semibold tracking-tight">About</h1>

    <div v-if="info" class="divide-y divide-line overflow-hidden rounded-xl border border-line bg-surface shadow-card">
      <!-- Identity -->
      <div class="flex items-center gap-4 px-4 py-5 sm:px-5">
        <svg class="shrink-0" width="52" height="52" viewBox="0 0 32 32" aria-hidden="true">
          <circle cx="16" cy="16" r="12" fill="none" stroke="#f97316" stroke-width="4.5" />
          <path d="M16 10a3.2 3.2 0 011.6 6L18.6 23h-5.2l1-7A3.2 3.2 0 0116 10z" fill="#0f2a44" class="dark:fill-slate-100" />
        </svg>
        <div class="min-w-0">
          <p class="text-[20px] font-semibold leading-tight">{{ info.name }}</p>
          <p class="mt-0.5 text-[13px] text-muted">A desktop front end for the system's OpenVPN client.</p>
        </div>
        <span class="ml-auto shrink-0 rounded-full bg-accent-soft px-3 py-1 text-[13px] font-medium text-accent-text">v{{ info.version }}</span>
      </div>

      <!-- Facts -->
      <dl class="divide-y divide-line">
        <div class="fact"><dt>OpenVPN</dt><dd>{{ info.openvpn }}</dd></div>
        <div class="fact"><dt>Platform</dt><dd>{{ info.platform }}</dd></div>
        <div class="fact"><dt>License</dt><dd>{{ info.license }}</dd></div>
        <div class="fact">
          <dt>Settings folder</dt>
          <dd class="flex flex-wrap items-center gap-2">
            <span class="break-all font-mono text-[13px]">{{ info.configDir }}</span>
            <button class="flex h-8 items-center gap-1.5 rounded-lg border border-line px-2.5 text-[13px] hover:bg-surface2" @click="SettingsService.OpenConfigFolder()">
              <Icon name="external" :size="14" />Open
            </button>
          </dd>
        </div>
      </dl>

      <!-- Notices -->
      <details class="group">
        <summary class="flex cursor-pointer list-none items-center justify-between px-4 py-4 hover:bg-surface2 sm:px-5">
          <span class="font-medium">Third-party notices <span class="ml-1 text-[13px] font-normal text-muted">({{ info.notices?.length ?? 0 }})</span></span>
          <Icon name="chevron" :size="16" class="rotate-90 text-muted transition-transform group-open:-rotate-90" />
        </summary>
        <ul class="space-y-2 border-t border-line bg-surface2/50 px-4 py-4 text-[13px] sm:px-5">
          <li v-for="n in info.notices" :key="n" class="flex gap-2"><span class="mt-[7px] h-1 w-1 shrink-0 rounded-full bg-muted" aria-hidden="true" /><span>{{ n }}</span></li>
        </ul>
      </details>
    </div>
  </div>
</template>

<style scoped>
.fact {
  display: grid;
  gap: 0.25rem 2rem;
  padding: 0.875rem 1rem;
  font-size: 14px;
}
.fact dt { color: var(--muted); }
@media (min-width: 640px) {
  .fact { grid-template-columns: 10rem minmax(0, 1fr); padding: 0.875rem 1.25rem; align-items: center; }
}
</style>
