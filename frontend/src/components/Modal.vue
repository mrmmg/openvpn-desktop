<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";

defineProps<{ title: string; width?: string }>();
const emit = defineEmits<{ (e: "close"): void }>();
const panel = ref<HTMLElement>();

function onKey(e: KeyboardEvent) {
  if (e.key === "Escape") emit("close");
}
onMounted(() => {
  window.addEventListener("keydown", onKey);
  panel.value?.querySelector<HTMLElement>("input,button.primary,button")?.focus();
});
onUnmounted(() => window.removeEventListener("keydown", onKey));
</script>

<template>
  <div class="fixed inset-0 z-40 flex items-center justify-center bg-black/40 p-4" @mousedown.self="emit('close')">
    <div
      ref="panel"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      class="selectable max-h-[90vh] w-full overflow-auto rounded-xl border border-line bg-surface p-5 shadow-card"
      :style="{ maxWidth: width ?? '28rem' }"
    >
      <h2 class="mb-3 text-base font-semibold">{{ title }}</h2>
      <slot />
      <div class="mt-5 flex justify-end gap-2"><slot name="actions" /></div>
    </div>
  </div>
</template>
