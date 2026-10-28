import { computed, ref } from "vue";
import { Events } from "@wailsio/runtime";
import {
  ConnectionService,
  LogsService,
  ProfilesService,
  SettingsService,
} from "../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";
import type {
  ConnectionState,
  ImportOutcome,
  LogEntry,
  Notice,
  Profile,
  Result,
  Settings,
  TrafficSample,
} from "../bindings/github.com/mrmmg/openvpn-desktop/internal/backend";

export type Theme = "light" | "dark" | "system";
export type View = "profiles" | "logs" | "settings" | "about";

// ---------------------------------------------------------------------------
// Theme. The backend config file is the source of truth; localStorage only
// caches the choice so the first paint does not flash the wrong theme.

const THEME_CACHE = "theme-cache";
const media = window.matchMedia("(prefers-color-scheme: dark)");

export const theme = ref<Theme>("system");
export const effectiveDark = ref(false);

function paint() {
  const dark = theme.value === "dark" || (theme.value === "system" && media.matches);
  effectiveDark.value = dark;
  document.documentElement.classList.toggle("dark", dark);
}

export function applyThemeCache() {
  try {
    const t = localStorage.getItem(THEME_CACHE);
    if (t === "light" || t === "dark" || t === "system") theme.value = t;
  } catch {
    /* storage unavailable */
  }
  paint();
  media.addEventListener("change", paint);
}

export async function setTheme(t: Theme) {
  theme.value = t;
  paint();
  try {
    localStorage.setItem(THEME_CACHE, t);
  } catch {
    /* ignore */
  }
  const r = await SettingsService.SetTheme(t);
  if (!r.ok) toast("error", "Could not save the theme", r.message);
}

// ---------------------------------------------------------------------------
// Toasts

export interface Toast {
  id: number;
  level: Notice["level"];
  title: string;
  message: string;
}
export const toasts = ref<Toast[]>([]);
let toastId = 0;

export function toast(level: Notice["level"], title: string, message = "") {
  const id = ++toastId;
  toasts.value.push({ id, level, title, message });
  setTimeout(() => dismissToast(id), level === "error" ? 9000 : 5000);
}
export function dismissToast(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id);
}

// ---------------------------------------------------------------------------
// Clock for uptime

export const now = ref(Date.now());
setInterval(() => (now.value = Date.now()), 1000);

// ---------------------------------------------------------------------------
// Application state

const idle: ConnectionState = {
  state: "disconnected", detail: "", activeId: "", starting: false, connectedSince: 0, phaseSince: 0,
  interface: "", vpnIp: "", serverHost: "", serverPort: "", serverIp: "", protocol: "", cipher: "", auth: "",
  dns: [], routes: [], publicIp: "", publicIpState: "off", bytesIn: 0, bytesOut: 0, error: "", errorHint: "",
  authFailed: false, message: "", reconnects: 0, credentials: null, external: [],
} as ConnectionState;

export const view = ref<View>("profiles");
export const menuOpen = ref(false);
export const profiles = ref<Profile[]>([]);
export const profilesLoaded = ref(false);
export const conn = ref<ConnectionState>({ ...idle });
export const traffic = ref<TrafficSample[]>([]);
export const logs = ref<LogEntry[]>([]);
export const settings = ref<Settings | null>(null);
/** Only one profile accordion is open at a time. */
export const openId = ref<string>("");

export const activeProfile = computed(() => profiles.value.find((p) => p.id === conn.value.activeId) ?? null);
/** True while a connection exists in any form (also while it is being set up). */
export const connectionBusy = computed(
  () => conn.value.starting || (conn.value.state !== "disconnected" && conn.value.state !== "error"),
);

export function isActive(id: string) {
  return conn.value.activeId === id && (connectionBusy.value || conn.value.state === "error");
}

async function loadProfiles() {
  try {
    profiles.value = (await ProfilesService.List()) ?? [];
  } catch (e) {
    toast("error", "Could not load profiles", String(e));
  }
  profilesLoaded.value = true;
}

export function reportOutcomes(list: ImportOutcome[] | null) {
  for (const o of list ?? []) {
    if (o.ok) {
      toast("success", `Imported ${o.profile?.name ?? o.file}`, (o.notes ?? []).join("\n"));
    } else {
      toast("error", `Could not import ${o.file}`, o.message);
    }
  }
}

export function handleResult(r: Result, title: string): boolean {
  if (!r.ok && r.code !== "cancelled") toast("error", title, r.message);
  return r.ok;
}

export async function browseImport() {
  reportOutcomes(await ProfilesService.Browse());
}

const MAX_LOG = 2000;
function pushLogs(list: LogEntry[]) {
  const all = logs.value.concat(list);
  logs.value = all.length > MAX_LOG ? all.slice(all.length - MAX_LOG * 0.75) : all;
}

export async function refreshSettings() {
  settings.value = await SettingsService.Get();
}

export async function init() {
  Events.On("connection:state", (e: any) => {
    const s = e.data as ConnectionState;
    const wasActive = conn.value.activeId;
    conn.value = s;
    if (s.state === "disconnected" && !s.activeId) traffic.value = [];
    if (s.activeId && s.activeId !== wasActive) openId.value = s.activeId;
  });
  Events.On("connection:traffic", (e: any) => {
    const t = traffic.value.concat(e.data as TrafficSample);
    traffic.value = t.length > 60 ? t.slice(t.length - 60) : t;
  });
  Events.On("app:notice", (e: any) => toast(e.data.level, e.data.title, e.data.message));
  Events.On("logs:append", (e: any) => pushLogs(e.data as LogEntry[]));
  Events.On("logs:reset", () => (logs.value = []));
  Events.On("profiles:changed", () => loadProfiles());
  Events.On("profiles:imported", (e: any) => reportOutcomes(e.data as ImportOutcome[]));

  // Files dropped anywhere else must not navigate the webview away.
  for (const ev of ["dragover", "drop"]) window.addEventListener(ev, (e) => e.preventDefault());

  const [s, tr, lg, st, warnings] = await Promise.all([
    ConnectionService.State(),
    ConnectionService.Traffic(),
    LogsService.Entries(),
    SettingsService.Get(),
    SettingsService.Startup(),
    loadProfiles(),
  ]).then((r) => r as unknown as [ConnectionState, TrafficSample[], LogEntry[], Settings, string[]]);
  conn.value = s;
  traffic.value = tr ?? [];
  logs.value = lg ?? [];
  settings.value = st;
  if (st.theme === "light" || st.theme === "dark" || st.theme === "system") {
    theme.value = st.theme;
    try {
      localStorage.setItem(THEME_CACHE, st.theme);
    } catch {
      /* ignore */
    }
    paint();
  }
  if (s.activeId) openId.value = s.activeId;
  for (const w of warnings ?? []) toast("warning", "Startup warning", w);
}
