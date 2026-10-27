<script setup lang="ts">
const props = defineProps<{ modelValue: boolean; disabled?: boolean; busy?: boolean; label: string }>();
const emit = defineEmits<{ (e: "toggle", next: boolean): void }>();
function click() {
  if (!props.disabled) emit("toggle", !props.modelValue);
}
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="modelValue"
    :aria-label="label"
    :title="label"
    :aria-disabled="disabled"
    class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors"
    :class="[
      modelValue ? 'border-accent bg-accent' : 'border-line bg-surface2',
      disabled ? 'opacity-60' : '',
    ]"
    @click.stop="click"
  >
    <span
      class="inline-flex h-5 w-5 items-center justify-center rounded-full bg-white shadow transition-transform"
      :class="modelValue ? 'translate-x-[22px]' : 'translate-x-[2px]'"
    >
      <svg v-if="busy" class="animate-spin text-muted" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" aria-hidden="true">
        <path d="M12 3a9 9 0 019 9" stroke-linecap="round" />
      </svg>
    </span>
  </button>
</template>
