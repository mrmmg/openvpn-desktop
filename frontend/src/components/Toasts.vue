<script setup lang="ts">
import { dismissToast, toasts } from "../store";
import Icon from "./Icon.vue";

const icon = { success: "check", error: "alert", warning: "alert", info: "about" } as Record<string, string>;
const tone = {
  success: "text-accent-text",
  error: "text-danger",
  warning: "text-warn",
  info: "text-muted",
} as Record<string, string>;
</script>

<template>
  <div class="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" aria-live="polite">
    <div
      v-for="t in toasts"
      :key="t.id"
      class="selectable pointer-events-auto flex gap-3 rounded-lg border border-line bg-surface p-3 shadow-card"
      :role="t.level === 'error' ? 'alert' : 'status'"
    >
      <span :class="tone[t.level]" class="mt-0.5"><Icon :name="icon[t.level] ?? 'about'" /></span>
      <div class="min-w-0 flex-1">
        <p class="font-medium">{{ t.title }}</p>
        <p v-if="t.message" class="mt-0.5 whitespace-pre-line break-words text-[13px] text-muted">{{ t.message }}</p>
      </div>
      <button class="text-muted hover:text-fg" aria-label="Dismiss" @click="dismissToast(t.id)"><Icon name="x" :size="16" /></button>
    </div>
  </div>
</template>
