<script setup lang="ts">
import { browseImport } from "../store";
import Icon from "./Icon.vue";
</script>

<template>
  <!-- Wails raises the native FilesDropped event for this element and adds
       .file-drop-target-active while files hover over it. -->
  <div
    data-file-drop-target
    class="flex items-center justify-center gap-3 rounded-xl border-2 border-dashed border-line bg-surface/60 px-4 py-5 text-[15px] transition-colors"
  >
    <Icon name="cloud" :size="26" class="text-muted" />
    <span>Drag &amp; drop an <span class="font-mono">.ovpn</span> file here or</span>
    <button class="font-medium text-accent-text underline-offset-2 hover:underline" @click="browseImport">Browse</button>
  </div>
</template>
