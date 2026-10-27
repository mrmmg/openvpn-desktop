<script setup lang="ts">
import { onMounted } from "vue";
import { init, view } from "./store";
import AppHeader from "./components/AppHeader.vue";
import Sidebar from "./components/Sidebar.vue";
import ProfilesView from "./components/ProfilesView.vue";
import LogsView from "./components/LogsView.vue";
import SettingsView from "./components/SettingsView.vue";
import AboutView from "./components/AboutView.vue";
import Toasts from "./components/Toasts.vue";
import CredentialsDialog from "./components/CredentialsDialog.vue";

onMounted(init);
</script>

<template>
  <div class="relative flex h-full flex-col overflow-hidden">
    <AppHeader />
    <main class="min-h-0 min-w-0 flex-1 overflow-auto p-4 sm:p-6">
      <ProfilesView v-if="view === 'profiles'" />
      <LogsView v-else-if="view === 'logs'" class="h-full" />
      <SettingsView v-else-if="view === 'settings'" />
      <AboutView v-else />
    </main>
    <Sidebar />
    <Toasts />
    <CredentialsDialog />
  </div>
</template>
