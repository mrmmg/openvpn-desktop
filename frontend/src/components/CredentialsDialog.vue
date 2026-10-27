<script setup lang="ts">
import { computed, ref } from "vue";
import { ConnectionService } from "../../bindings/github.com/mrmmg/openvpn-wails/internal/backend";
import { conn, handleResult } from "../store";
import Modal from "./Modal.vue";

const username = ref("");
const password = ref("");
const busy = ref(false);
const req = computed(() => conn.value.credentials);
const failed = computed(() => conn.value.authFailed);

async function submit() {
  if (busy.value) return;
  busy.value = true;
  const r = await ConnectionService.SubmitCredentials(username.value, password.value);
  // The secret never stays in component state longer than needed.
  password.value = "";
  busy.value = false;
  handleResult(r, "Could not send credentials");
}
async function cancel() {
  password.value = "";
  handleResult(await ConnectionService.CancelCredentials(), "Could not cancel");
}
</script>

<template>
  <Modal v-if="req" :title="req.needUsername ? 'VPN credentials' : `${req.type} required`" @close="cancel">
    <form class="flex flex-col gap-3" autocomplete="off" @submit.prevent="submit">
      <p v-if="failed" class="rounded-md bg-danger-soft px-3 py-2 text-[13px] text-danger" role="alert">Authentication failed. Check your credentials and try again.</p>
      <p v-if="req.challenge" class="text-[13px] text-muted">{{ req.challenge }}</p>
      <label v-if="req.needUsername" class="text-[13px] text-muted">
        Username
        <input v-model="username" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-[14px] text-fg" autocomplete="off" spellcheck="false" required />
      </label>
      <label class="text-[13px] text-muted">
        {{ req.needUsername ? "Password" : req.type }}
        <input v-model="password" type="password" class="mt-1 w-full rounded-md border border-line bg-bg px-3 py-2 text-[14px] text-fg" autocomplete="off" required />
      </label>
      <p class="text-[12px] text-muted">Credentials are sent straight to OpenVPN and are not saved.</p>
      <div class="mt-2 flex justify-end gap-2">
        <button type="button" class="rounded-lg border border-line px-4 py-2 text-[14px]" @click="cancel">Cancel</button>
        <button type="submit" class="primary rounded-lg bg-accent px-4 py-2 text-[14px] font-medium text-white disabled:opacity-60" :disabled="busy">Connect</button>
      </div>
    </form>
  </Modal>
</template>
