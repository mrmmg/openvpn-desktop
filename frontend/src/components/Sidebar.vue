<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from "vue";
import { activeProfile, conn, menuOpen, settings, view, type View } from "../store";
import Icon from "./Icon.vue";

const items: { id: View; label: string }[] = [
  { id: "profiles", label: "Profiles" },
  { id: "logs", label: "Logs" },
  { id: "settings", label: "Settings" },
  { id: "about", label: "About" },
];

const summary = computed(() => {
  const s = conn.value;
  const name = activeProfile.value?.name;
  switch (s.state) {
    case "connected": return { dot: "bg-accent", label: "Connected", sub: name ?? "Active connection" };
    case "connecting": return { dot: "bg-warn pulse", label: "Connecting…", sub: name ?? "" };
    case "disconnecting": return { dot: "bg-warn pulse", label: "Disconnecting…", sub: name ?? "" };
    case "error": return { dot: "bg-danger", label: "Connection failed", sub: name ?? "" };
    case "unknown": return { dot: "bg-warn pulse", label: "Checking…", sub: "" };
  }
  return s.starting
    ? { dot: "bg-warn pulse", label: "Starting…", sub: name ?? "" }
    : { dot: "bg-muted/60", label: "Disconnected", sub: "No active connection" };
});

function go(id: View) {
  view.value = id;
  menuOpen.value = false;
}
function key(e: KeyboardEvent) {
  if (e.key === "Escape") menuOpen.value = false;
}
onMounted(() => window.addEventListener("keydown", key));
onBeforeUnmount(() => window.removeEventListener("keydown", key));
</script>

<template>
  <!-- Slide-over menu, hidden until the hamburger button is pressed. -->
  <div class="pointer-events-none absolute inset-x-0 bottom-0 top-14 z-20 overflow-hidden" :aria-hidden="!menuOpen">
    <div
      class="absolute inset-0 bg-black/40 transition-opacity duration-200"
      :class="menuOpen ? 'pointer-events-auto opacity-100' : 'opacity-0'"
      @click="menuOpen = false"
    />
    <aside
      id="main-menu"
      class="absolute inset-y-0 left-0 flex w-64 max-w-[85%] flex-col justify-between border-r border-line bg-surface p-3 shadow-card transition-transform duration-200"
      :class="menuOpen ? 'pointer-events-auto translate-x-0' : '-translate-x-full'"
      :inert="!menuOpen"
    >
      <nav class="flex flex-col gap-1" aria-label="Main">
        <button
          v-for="i in items"
          :key="i.id"
          class="relative flex items-center gap-3 rounded-lg px-3 py-3 text-left text-[15px] transition-colors"
          :class="view === i.id ? 'bg-accent-soft font-medium text-accent-text' : 'text-fg hover:bg-surface2'"
          :aria-current="view === i.id ? 'page' : undefined"
          @click="go(i.id)"
        >
          <span v-if="view === i.id" class="absolute bottom-2 left-0 top-2 w-1 rounded-r bg-accent" aria-hidden="true" />
          <Icon :name="i.id" :size="22" />{{ i.label }}
        </button>
      </nav>

      <div>
        <div class="rounded-lg bg-surface2 p-3" role="status" aria-live="polite">
          <div class="flex items-center gap-2 text-[13px] font-medium">
            <span class="h-2.5 w-2.5 shrink-0 rounded-full" :class="summary.dot" aria-hidden="true" />
            {{ summary.label }}
          </div>
          <p class="mt-1 truncate pl-[18px] text-xs text-muted">{{ summary.sub }}</p>
        </div>
        <p class="mt-3 px-1 text-xs text-muted">v{{ settings?.version }}</p>
      </div>
    </aside>
  </div>
</template>
