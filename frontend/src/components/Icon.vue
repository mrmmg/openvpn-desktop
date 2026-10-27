<script setup lang="ts">
import { type Component } from "vue";
import Home from "~icons/hugeicons/home-01";
import Logs from "~icons/hugeicons/task-01";
import Settings from "~icons/hugeicons/settings-02";
import About from "~icons/hugeicons/information-circle";
import Chevron from "~icons/hugeicons/arrow-right-01";
import File from "~icons/hugeicons/file-01";
import Dots from "~icons/hugeicons/more-vertical";
import Sun from "~icons/hugeicons/sun-03";
import Moon from "~icons/hugeicons/moon-02";
import Cloud from "~icons/hugeicons/cloud-upload";
import Down from "~icons/hugeicons/arrow-down-02";
import Up from "~icons/hugeicons/arrow-up-02";
import Power from "~icons/hugeicons/power";
import Copy from "~icons/hugeicons/copy-01";
import Trash from "~icons/hugeicons/delete-02";
import Edit from "~icons/hugeicons/edit-02";
import Folder from "~icons/hugeicons/folder-01";
import Check from "~icons/hugeicons/tick-02";
import X from "~icons/hugeicons/cancel-01";
import Alert from "~icons/hugeicons/alert-02";
import Shield from "~icons/hugeicons/shield-01";
import Download from "~icons/hugeicons/download-01";
import External from "~icons/hugeicons/square-arrow-up-right";
import Refresh from "~icons/hugeicons/refresh";
import Minus from "~icons/hugeicons/minus-sign";
import Menu from "~icons/hugeicons/menu-01";

defineProps<{ name: string; size?: number }>();
// Names used by the app -> Hugeicons (https://hugeicons.com).
const icons: Record<string, Component> = {
  profiles: Home, logs: Logs, settings: Settings, about: About, chevron: Chevron, file: File, dots: Dots,
  sun: Sun, moon: Moon, cloud: Cloud, down: Down, up: Up, power: Power, copy: Copy, trash: Trash, edit: Edit,
  folder: Folder, check: Check, x: X, alert: Alert, shield: Shield, download: Download, external: External,
  refresh: Refresh, minus: Minus, menu: Menu,
};
</script>

<template>
  <component :is="icons[name]" :width="size ?? 18" :height="size ?? 18" aria-hidden="true" />
</template>
