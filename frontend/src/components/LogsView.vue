<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { LogsService } from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import { handleResult, logs, settings, toast } from "../store";
import Icon from "./Icon.vue";
import UiCheckbox from "./UiCheckbox.vue";
import UiInput from "./UiInput.vue";
import UiSelect from "./UiSelect.vue";

const level = ref<"all" | "warn" | "error">("all");
const query = ref("");
const follow = ref(true);
const box = ref<HTMLElement>();

const rank = { debug: 0, info: 1, warn: 2, error: 3 } as Record<string, number>;
const shown = computed(() => {
  const min = level.value === "all" ? 0 : level.value === "warn" ? 2 : 3;
  const q = query.value.trim().toLowerCase();
  return logs.value.filter((e) => rank[e.level] >= min && (!q || e.message.toLowerCase().includes(q)));
});

watch(
  () => shown.value.length,
  async () => {
    if (!follow.value) return;
    await nextTick();
    if (box.value) box.value.scrollTop = box.value.scrollHeight;
  },
  { flush: "post" },
);

const tone: Record<string, string> = { error: "text-danger", warn: "text-warn", info: "text-muted", debug: "text-muted/70" };

function line(e: (typeof logs.value)[number]) {
  return `${e.time} [${e.level.toUpperCase()}] ${e.source}: ${e.message}`;
}
async function copy() {
  try {
    await navigator.clipboard.writeText(shown.value.map(line).join("\n"));
    toast("success", "Copied", `${shown.value.length} lines`);
  } catch {
    toast("error", "Could not copy to the clipboard");
  }
}
async function exportLog() {
  const r = await LogsService.Export();
  if (r.ok) toast("success", "Log exported");
  else handleResult(r, "Export failed");
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-3">
    <div class="flex flex-wrap items-end justify-between gap-2">
      <div>
        <h1 class="text-[26px] font-semibold tracking-tight">Logs</h1>
        <p class="mt-1 flex items-center gap-2 text-[13px] text-muted">
          <span class="inline-flex items-center gap-1.5 rounded-full bg-accent-soft px-2 py-0.5 font-medium text-accent-text"><span class="h-1.5 w-1.5 rounded-full bg-accent pulse" />Live</span>
          Streaming from OpenVPN and the app. The full OpenVPN log is also saved on disk.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2 text-[13px]">
        <UiSelect
          v-model="level"
          label="Severity filter"
          width="11.5rem"
          :options="[
            { value: 'all', label: 'All levels' },
            { value: 'warn', label: 'Warnings and errors' },
            { value: 'error', label: 'Errors only' },
          ]"
        />
        <div class="w-48"><UiInput v-model="query" label="Filter text" placeholder="Filter text…" /></div>
        <UiCheckbox v-model="follow" label="Follow" />
        <button class="flex items-center gap-1.5 h-10 rounded-lg border border-line bg-surface px-3 hover:bg-surface2" @click="copy"><Icon name="copy" :size="15" />Copy</button>
        <button class="flex items-center gap-1.5 h-10 rounded-lg border border-line bg-surface px-3 hover:bg-surface2" @click="exportLog"><Icon name="download" :size="15" />Export</button>
        <button class="flex items-center gap-1.5 h-10 rounded-lg border border-line bg-surface px-3 hover:bg-surface2" @click="LogsService.OpenLogFolder()"><Icon name="folder" :size="15" />Saved logs</button>
        <button class="flex items-center gap-1.5 h-10 rounded-lg border border-line bg-surface px-3 hover:bg-surface2" @click="LogsService.Clear()"><Icon name="trash" :size="15" />Clear</button>
      </div>
    </div>

    <div ref="box" class="selectable min-h-0 flex-1 overflow-auto rounded-xl border border-line bg-surface p-3 font-mono text-[12px] leading-5 shadow-card" role="log" aria-live="off" tabindex="0">
      <p v-if="shown.length === 0" class="font-sans text-[13px] text-muted">
        {{ logs.length ? "No lines match the filter." : "Nothing logged yet. Connect a profile to see OpenVPN's output here." }}
      </p>
      <div v-for="e in shown" :key="e.id" class="flex gap-2 whitespace-pre-wrap break-all">
        <span class="shrink-0 text-muted">{{ e.time.length > 19 && e.time.includes("T") ? e.time.slice(11, 19) : e.time }}</span>
        <span class="w-12 shrink-0 font-semibold" :class="tone[e.level]">{{ e.level.toUpperCase() }}</span>
        <span class="hidden w-14 shrink-0 text-muted sm:inline">{{ e.source }}</span>
        <span :class="e.level === 'error' ? 'text-danger' : ''">{{ e.message }}</span>
      </div>
    </div>
    <p class="text-[12px] text-muted">Showing {{ shown.length }} of {{ logs.length }} buffered lines (last 2000 kept). App log: <span class="font-mono">{{ settings?.logFile }}</span></p>
  </div>
</template>
