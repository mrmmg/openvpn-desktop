<script setup lang="ts">
defineProps<{ modelValue: string; label: string; placeholder?: string; type?: string; mono?: boolean; invalid?: boolean }>();
const emit = defineEmits<{ (e: "update:modelValue", v: string): void; (e: "enter"): void }>();
</script>

<template>
  <input
    :value="modelValue"
    :type="type ?? 'text'"
    :aria-label="label"
    :aria-invalid="invalid"
    :placeholder="placeholder"
    spellcheck="false"
    class="h-10 w-full min-w-0 rounded-lg border bg-surface px-3 text-[14px] transition-colors placeholder:text-muted/70 hover:border-muted/50 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/20"
    :class="[mono ? 'font-mono text-[13px]' : '', invalid ? 'border-danger' : 'border-line']"
    @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    @keydown.enter="emit('enter')"
  />
</template>
