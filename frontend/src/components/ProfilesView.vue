<script setup lang="ts">
import { ref } from "vue";
import {
  ConnectionService,
  ProfilesService,
} from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import type { Profile, ProfileDetails } from "../../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import { activeProfile, browseImport, conn, connectionBusy, handleResult, openId, profiles, profilesLoaded, toast } from "../store";
import Icon from "./Icon.vue";
import ImportZone from "./ImportZone.vue";
import Modal from "./Modal.vue";
import ProfileCard from "./ProfileCard.vue";
import ProfileDetailsDialog from "./ProfileDetailsDialog.vue";

const blocked = ref<Profile | null>(null);
const scripts = ref<{ profile: Profile; message: string } | null>(null);
const removing = ref<Profile | null>(null);
const details = ref<ProfileDetails | null>(null);

async function connect(p: Profile, acknowledge = false, askCredentials = false) {
  const r = await ConnectionService.Connect(p.id, acknowledge, askCredentials);
  if (r.ok) {
    openId.value = p.id;
    return;
  }
  if (r.code === "scripts") scripts.value = { profile: p, message: r.message };
  else if (r.code === "busy") blocked.value = p;
  else handleResult(r, `Could not connect ${p.name}`);
}

async function connectWithPrompt(p: Profile) {
  if (connectionBusy.value) {
    blocked.value = p;
    return;
  }
  await connect(p, false, true);
}

async function onToggle(p: Profile, next: boolean) {
  if (next) {
    // Never switch silently: another connection has to be closed first.
    if (connectionBusy.value && conn.value.activeId !== p.id) {
      blocked.value = p;
      return;
    }
    await connect(p);
  } else {
    handleResult(await ConnectionService.Disconnect(), "Could not disconnect");
  }
}

async function rename(p: Profile, name: string) {
  handleResult(await ProfilesService.Rename(p.id, name), "Could not rename");
}
async function confirmRemove() {
  const p = removing.value;
  removing.value = null;
  if (p && handleResult(await ProfilesService.Remove(p.id), "Could not remove")) {
    if (openId.value === p.id) openId.value = "";
    toast("info", `Removed ${p.name}`, "The original .ovpn file was not touched.");
  }
}
async function showDetails(p: Profile) {
  try {
    details.value = await ProfilesService.Details(p.id);
  } catch (e) {
    toast("error", "Could not read the profile", String(e));
  }
}
async function reveal(p: Profile) {
  handleResult(await ProfilesService.RevealFolder(p.id), "Could not open the folder");
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <h1 class="text-[26px] font-semibold tracking-tight">Profiles</h1>
    <ImportZone />

    <div v-if="profilesLoaded && profiles.length === 0" class="flex flex-col items-center gap-3 rounded-xl border border-line bg-surface px-6 py-12 text-center shadow-card">
      <Icon name="shield" :size="40" class="text-muted" />
      <p class="text-[17px] font-semibold">No VPN profiles yet</p>
      <p class="max-w-md text-[14px] text-muted">Import an <span class="font-mono">.ovpn</span> file from your VPN provider or administrator. Certificates and keys referenced next to it are copied along, and your original files are never changed.</p>
      <button class="mt-1 rounded-lg bg-accent px-4 py-2 text-[14px] font-medium text-white hover:opacity-90" @click="browseImport">Import a profile</button>
    </div>

    <ProfileCard
      v-for="p in profiles"
      :key="p.id"
      :profile="p"
      @toggle="onToggle(p, $event)"
      @prompt="connectWithPrompt(p)"
      @rename="rename(p, $event)"
      @remove="removing = p"
      @details="showDetails(p)"
      @reveal="reveal(p)"
    />

    <Modal v-if="blocked" title="Another connection is active" @close="blocked = null">
      <p class="text-[14px]">
        <span class="font-semibold">{{ activeProfile?.name ?? "A VPN connection" }}</span> is still {{ conn.state === "connected" ? "connected" : "in use" }}.
        Disconnect it first, then connect <span class="font-semibold">{{ blocked.name }}</span>. Only one VPN connection can be active at a time.
      </p>
      <template #actions>
        <button class="primary rounded-lg border border-line px-4 py-2 text-[14px]" @click="blocked = null">OK</button>
      </template>
    </Modal>

    <Modal v-if="scripts" title="This profile runs commands" @close="scripts = null">
      <p class="mb-2 text-[14px]">{{ scripts.profile.name }} asks OpenVPN to run programs with administrator rights. Only continue if you trust where this profile came from.</p>
      <pre class="selectable max-h-40 overflow-auto rounded-md bg-surface2 p-2 font-mono text-[12px]">{{ scripts.message }}</pre>
      <template #actions>
        <button class="rounded-lg border border-line px-4 py-2 text-[14px]" @click="scripts = null">Cancel</button>
        <button class="rounded-lg bg-danger px-4 py-2 text-[14px] font-medium text-white" @click="(() => { const s = scripts!; scripts = null; connect(s.profile, true); })()">Connect anyway</button>
      </template>
    </Modal>

    <Modal v-if="removing" title="Remove profile?" @close="removing = null">
      <p class="text-[14px]">Remove <span class="font-semibold">{{ removing.name }}</span> and the copies of its certificates and keys stored by this app? The original <span class="font-mono">{{ removing.file }}</span> is not touched.</p>
      <template #actions>
        <button class="primary rounded-lg border border-line px-4 py-2 text-[14px]" @click="removing = null">Cancel</button>
        <button class="rounded-lg bg-danger px-4 py-2 text-[14px] font-medium text-white" @click="confirmRemove">Remove</button>
      </template>
    </Modal>

    <ProfileDetailsDialog v-if="details" :details="details" @close="details = null" />
  </div>
</template>
