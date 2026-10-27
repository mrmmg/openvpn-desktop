<script setup lang="ts">
import { computed, nextTick, ref } from "vue";
import type { Profile } from "../../bindings/github.com/mrmmg/openvpn-wails/internal/backend";
import { conn, connectionBusy, isActive, openId } from "../store";
import { formatLastConnected as formatLast } from "../format";
import Icon from "./Icon.vue";
import Toggle from "./Toggle.vue";
import ConnectionPanel from "./ConnectionPanel.vue";

const props = defineProps<{ profile: Profile }>();
const emit = defineEmits<{
  (e: "toggle", next: boolean): void;
  (e: "rename", name: string): void;
  (e: "remove"): void;
  (e: "details"): void;
  (e: "prompt"): void;
  (e: "reveal"): void;
}>();

const active = computed(() => isActive(props.profile.id));
// Opening a profile closes the previously open one; a connecting/connected
// profile opens itself (see store).
const open = computed(() => openId.value === props.profile.id);
const s = computed(() => (active.value ? conn.value : null));

const on = computed(() => !!s.value && (s.value.state === "connected" || s.value.state === "connecting" || s.value.starting));
const transitional = computed(
  () => !!s.value && (s.value.starting || s.value.state === "disconnecting" || s.value.state === "unknown"),
);

const badge = computed(() => {
  const st = s.value;
  if (!st) return null;
  if (st.starting && st.state === "disconnected") return { text: "Starting…", dot: "bg-warn pulse", tone: "text-warn" };
  switch (st.state) {
    case "connected": return { text: "Connected", dot: "bg-accent", tone: "text-accent-text" };
    case "connecting": return { text: "Connecting…", dot: "bg-warn pulse", tone: "text-warn" };
    case "disconnecting": return { text: "Disconnecting…", dot: "bg-warn pulse", tone: "text-warn" };
    case "error": return { text: "Failed", dot: "bg-danger", tone: "text-danger" };
    case "unknown": return { text: "Checking…", dot: "bg-warn pulse", tone: "text-warn" };
  }
  return null;
});

function toggleOpen() {
  openId.value = open.value ? "" : props.profile.id;
}

// Menu
const menu = ref(false);
function closeMenu() {
  menu.value = false;
  window.removeEventListener("click", closeMenu);
}
function openMenu() {
  menu.value = !menu.value;
  if (menu.value) setTimeout(() => window.addEventListener("click", closeMenu), 0);
}

// Inline rename
const editing = ref(false);
const draft = ref("");
const input = ref<HTMLInputElement>();
async function startRename() {
  closeMenu();
  draft.value = props.profile.name;
  editing.value = true;
  await nextTick();
  input.value?.select();
}
function commit() {
  if (!editing.value) return;
  editing.value = false;
  const v = draft.value.trim();
  if (v && v !== props.profile.name) emit("rename", v);
}
</script>

<template>
  <section
    class="relative rounded-xl border bg-surface shadow-card transition-colors"
    :class="[active && s?.state === 'connected' ? 'border-accent' : 'border-line', menu ? 'z-20' : '']"
  >
    <div class="flex items-center gap-2 px-2 py-3 sm:gap-3 sm:px-4">
      <button
        class="flex h-8 w-8 items-center justify-center rounded-md text-muted hover:bg-surface2"
        :aria-expanded="open"
        :aria-label="(open ? 'Collapse ' : 'Expand ') + profile.name"
        @click="toggleOpen"
      >
        <Icon name="chevron" :size="18" class="transition-transform" :class="open ? 'rotate-90' : ''" />
      </button>

      <span class="flex shrink-0 items-center gap-2" :title="badge?.text ?? 'Disconnected'" role="img" :aria-label="badge?.text ?? 'Disconnected'">
        <span class="h-2.5 w-2.5 shrink-0 rounded-full" :class="badge?.dot ?? 'bg-muted/50'" aria-hidden="true" />
        <span v-if="badge" class="hidden w-[6.5rem] truncate text-[12px] font-medium min-[520px]:inline" :class="badge.tone">{{ badge.text }}</span>
      </span>

      <Icon name="file" :size="22" class="hidden shrink-0 text-muted sm:block" />

      <div class="min-w-0 flex-1">
        <input
          v-if="editing"
          ref="input"
          v-model="draft"
          class="w-full rounded-md border border-accent bg-bg px-2 py-0.5 text-[16px] font-semibold"
          maxlength="80"
          aria-label="Profile name"
          @keydown.enter="commit"
          @keydown.esc="editing = false"
          @blur="commit"
        />
        <p v-else class="truncate text-[16px] font-semibold" :title="'Double-click to rename'" @dblclick="startRename">{{ profile.name }}</p>
        <p class="truncate text-[13px] text-muted">{{ profile.file }}</p>
        <p v-if="profile.server" class="truncate font-mono text-[12px] text-muted sm:hidden">{{ profile.server }}</p>
      </div>

      <span
        v-if="profile.server"
        class="hidden max-w-[16rem] shrink truncate rounded-md px-3 py-1.5 font-mono text-[13px] sm:inline-block"
        :class="on ? 'bg-accent-soft text-accent-text' : 'bg-surface2 text-muted'"
        :title="profile.server"
      >{{ profile.server }}</span>
      <span v-else-if="profile.problem" class="inline-flex items-center gap-1 text-[12px] text-danger" :title="profile.problem"><Icon name="alert" :size="14" />Invalid</span>

      <Toggle
        :model-value="on"
        :disabled="transitional"
        :busy="transitional"
        :label="on ? `Disconnect ${profile.name}` : `Connect ${profile.name}`"
        @toggle="emit('toggle', $event)"
      />

      <div class="relative">
        <button class="flex h-8 w-8 items-center justify-center rounded-md text-muted hover:bg-surface2" aria-label="Profile actions" aria-haspopup="menu" :aria-expanded="menu" @click.stop="openMenu">
          <Icon name="dots" />
        </button>
        <div v-if="menu" role="menu" class="absolute right-0 top-9 z-20 w-52 overflow-hidden rounded-lg border border-line bg-surface py-1 text-[14px] shadow-card">
          <button role="menuitem" class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-surface2" @click="startRename"><Icon name="edit" :size="16" />Rename</button>
          <button v-if="profile.auth === 'file'" role="menuitem" class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-surface2 disabled:opacity-50" :disabled="connectionBusy" @click="closeMenu(); emit('prompt')"><Icon name="shield" :size="16" />Connect with other credentials…</button>
          <button role="menuitem" class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-surface2" @click="emit('details')"><Icon name="about" :size="16" />Configuration details</button>
          <button role="menuitem" class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-surface2" @click="emit('reveal')"><Icon name="folder" :size="16" />Show in folder</button>
          <button role="menuitem" class="flex w-full items-center gap-2 px-3 py-2 text-left text-danger hover:bg-surface2 disabled:opacity-50" :disabled="active && connectionBusy" @click="emit('remove')"><Icon name="trash" :size="16" />Remove</button>
        </div>
      </div>
    </div>

    <!-- Expanded: live details for the active profile, a static summary otherwise -->
    <ConnectionPanel v-if="open && active" :profile="profile" @retry="emit('toggle', true)" @prompt="emit('prompt')" />
    <div v-else-if="open" class="selectable grid gap-x-6 gap-y-3 border-t border-line px-5 py-4 text-[13px] sm:grid-cols-2 lg:grid-cols-4">
      <div><p class="text-muted">Server</p><p class="mt-0.5 break-all font-medium">{{ profile.server || "Not available" }}</p></div>
      <div><p class="text-muted">Protocol</p><p class="mt-0.5 font-medium">{{ profile.protocol || "Not available" }}</p></div>
      <div>
        <p class="text-muted">Sign-in</p>
        <p class="mt-0.5 font-medium">{{ profile.auth === "prompt" ? "Asks on connect" : profile.auth === "file" ? "Saved credential file (next to the profile)" : "None" }}</p>
      </div>
      <div><p class="text-muted">Last connected</p><p class="mt-0.5 font-medium">{{ formatLast(profile.lastConnected) }}</p></div>
      <p v-if="profile.risks?.length" class="col-span-full flex items-center gap-2 rounded-md bg-warn-soft px-3 py-2 text-warn">
        <Icon name="alert" :size="16" />Runs external commands as administrator when connecting.
      </p>
      <p v-if="profile.problem" class="col-span-full rounded-md bg-danger-soft px-3 py-2 text-danger">{{ profile.problem }}</p>
      <p class="col-span-full text-muted">Not connected — no traffic or connection details to show.</p>
    </div>
  </section>
</template>
