<script setup lang="ts">
import Icon from "./Icon.vue";
import Toggle from "./Toggle.vue";
import UiInput from "./UiInput.vue";

defineProps<{ name: string; url: string; enabled: boolean }>();
const emit = defineEmits<{
  (e: "name", v: string): void;
  (e: "url", v: string): void;
  (e: "enabled", v: boolean): void;
  (e: "remove"): void;
}>();
</script>

<template>
  <li class="flex flex-wrap items-center gap-2 sm:flex-nowrap">
    <Toggle :model-value="enabled" :label="(enabled ? 'Disable ' : 'Enable ') + (name || 'service')" @toggle="emit('enabled', $event)" />
    <UiInput class="!w-36 shrink-0" :model-value="name" label="Service name" placeholder="Name" @update:model-value="emit('name', $event)" />
    <UiInput class="flex-1" :model-value="url" mono label="Service URL" placeholder="https://api.example.com/ip" @update:model-value="emit('url', $event)" />
    <button
      class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-line text-muted hover:bg-surface2 hover:text-danger"
      :aria-label="'Remove ' + (name || 'service')"
      @click="emit('remove')"
    >
      <Icon name="trash" :size="16" />
    </button>
  </li>
</template>
