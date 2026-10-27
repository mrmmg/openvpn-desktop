const UNITS = ["B", "KB", "MB", "GB", "TB"];

/** Splits a byte count into a number and unit, e.g. 1536 -> ["1.50", "KB"]. */
export function splitBytes(n: number): [string, string] {
  if (!isFinite(n) || n < 0) n = 0;
  let i = 0;
  while (n >= 1000 && i < UNITS.length - 1) {
    n /= 1000;
    i++;
  }
  const digits = i === 0 ? 0 : n >= 100 ? 0 : n >= 10 ? 1 : 2;
  return [n.toFixed(digits), UNITS[i]];
}

export function formatBytes(n: number): string {
  const [v, u] = splitBytes(n);
  return `${v} ${u}`;
}

export function splitRate(bytesPerSec: number): [string, string] {
  const [v, u] = splitBytes(bytesPerSec);
  return [v, `${u}/s`];
}

export function formatRate(bytesPerSec: number): string {
  const [v, u] = splitRate(bytesPerSec);
  return `${v} ${u}`;
}

/** hh:mm:ss (hours may exceed 24). */
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const p = (x: number) => String(x).padStart(2, "0");
  return `${p(h)}:${p(m)}:${p(s % 60)}`;
}

export function formatLastConnected(ms: number): string {
  if (!ms) return "Never";
  const d = new Date(ms);
  const now = new Date();
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.toDateString() === now.toDateString()) return `Today, ${time}`;
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return `Yesterday, ${time}`;
  return `${d.toLocaleDateString()} ${time}`;
}
