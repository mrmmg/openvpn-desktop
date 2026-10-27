<script setup lang="ts">
import Icon from "./Icon.vue";

defineProps<{ modelValue: boolean; label: string; hint?: string }>();
const emit = defineEmits<{ (e: "update:modelValue", v: boolean): void }>();
</script>

<template>
  <label class="group flex cursor-pointer items-start gap-3 text-[14px]">
    <input
      type="checkbox"
      class="peer sr-only"
      :checked="modelValue"
      @change="emit('update:modelValue', ($event.target as HTMLInputElement).checked)"
    />
    <span
      class="mt-px flex h-5 w-5 shrink-0 items-center justify-center rounded-md border-2 transition-colors peer-focus-visible:outline peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-accent"
      :class="modelValue ? 'border-accent bg-accent text-white' : 'border-muted/60 bg-surface group-hover:border-accent'"
      aria-hidden="true"
    >
      <Icon v-if="modelValue" name="check" :size="14" />
    </span>
    <span>
      <span class="font-medium">{{ label }}</span>
      <span v-if="hint" class="mt-0.5 block text-[13px] font-normal text-muted">{{ hint }}</span>
    </span>
  </label>
</template>
