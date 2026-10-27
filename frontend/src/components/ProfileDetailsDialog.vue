<script setup lang="ts">
import type { ProfileDetails } from "../../bindings/github.com/mrmmg/openvpn-wails/internal/backend";
import Modal from "./Modal.vue";

defineProps<{ details: ProfileDetails }>();
const emit = defineEmits<{ (e: "close"): void }>();

const rows = (d: ProfileDetails): [string, string[]][] => [
  ["Servers", d.remotes ?? []],
  ["Device", d.device ? [d.device] : []],
  ["Authentication", [d.auth === "prompt" ? "Asks for username/password on connect" : d.auth === "file" ? "Uses the saved credential file next to the profile (<name>.txt)" : "No username/password"]],
  ["Referenced files", d.files ?? []],
  ["Missing files", d.missing ?? []],
  ["Routing directives", d.routing ?? []],
  ["External commands", d.scripts ?? []],
  ["Folder", [d.directory]],
];
</script>

<template>
  <Modal :title="details.name" width="40rem" @close="emit('close')">
    <p class="mb-3 text-[13px] text-muted">Imported from <span class="font-mono">{{ details.file }}</span>. Keys and certificates are not shown.</p>
    <dl class="flex flex-col gap-3 text-[13px]">
      <template v-for="[label, values] in rows(details)" :key="label">
        <div v-if="values.length">
          <dt class="text-muted">{{ label }}</dt>
          <dd v-for="v in values" :key="v" class="break-all font-mono" :class="label === 'Missing files' || label === 'External commands' ? 'text-warn' : ''">{{ v }}</dd>
        </div>
      </template>
    </dl>
    <template #actions>
      <button class="primary rounded-lg border border-line px-4 py-2 text-[14px]" @click="emit('close')">Close</button>
    </template>
  </Modal>
</template>
