<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import { effectiveDark, traffic } from "../store";
import { splitRate } from "../format";

const POINTS = 60;
const el = ref<HTMLDivElement>();
let plot: uPlot | null = null;
let ro: ResizeObserver | null = null;

const css = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

function data(): uPlot.AlignedData {
  const xs = Array.from({ length: POINTS }, (_, i) => i - POINTS + 1);
  const down: (number | null)[] = new Array(POINTS).fill(null);
  const up: (number | null)[] = new Array(POINTS).fill(null);
  const s = traffic.value.slice(-POINTS);
  s.forEach((p, i) => {
    down[POINTS - s.length + i] = p.down;
    up[POINTS - s.length + i] = p.up;
  });
  return [xs, down, up];
}

function area(color: string) {
  return (u: uPlot) => {
    const h = u.bbox.height;
    const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + h);
    g.addColorStop(0, color + "66");
    g.addColorStop(1, color + "05");
    return g;
  };
}

function build() {
  if (!el.value) return;
  plot?.destroy();
  const down = css("--down"), up = css("--up"), muted = css("--muted"), line = css("--line");
  const axis = { stroke: muted, grid: { stroke: line, width: 1 }, ticks: { show: false }, font: "12px system-ui" };
  plot = new uPlot(
    {
      width: el.value.clientWidth,
      height: el.value.clientHeight || 150,
      legend: { show: false },
      cursor: { show: false },
      padding: [8, 4, 0, 14],
      scales: {
        x: { time: false, range: [-POINTS, 0] },
        y: { range: (_u, _min, max) => [0, Math.max(max * 1.25, 10_000)] },
      },
      axes: [
        {
          ...axis,
          splits: () => [-60, -45, -30, -15, 0],
          values: (_u, v) => v.map((x) => (x === 0 ? "Now" : `${-x}s`)),
          gap: 6,
        },
        {
          ...axis,
          side: 1,
          size: 74,
          splits: (u) => {
            const max = u.scales.y.max ?? 0;
            return [0, max / 2, max];
          },
          values: (_u, v) => v.map((x) => (x === 0 ? "0" : splitRate(x).join(" "))),
        },
      ],
      series: [
        {},
        { label: "Download", stroke: down, width: 2, fill: area(down), points: { show: false } },
        { label: "Upload", stroke: up, width: 2, fill: area(up), points: { show: false } },
      ],
    },
    data(),
    el.value,
  );
}

onMounted(() => {
  build();
  ro = new ResizeObserver(() => {
    if (plot && el.value) plot.setSize({ width: el.value.clientWidth, height: el.value.clientHeight });
  });
  if (el.value) ro.observe(el.value);
});
onBeforeUnmount(() => {
  ro?.disconnect();
  plot?.destroy();
  plot = null;
});
watch(traffic, () => plot?.setData(data()));
watch(effectiveDark, () => requestAnimationFrame(build));
</script>

<template>
  <div ref="el" class="h-[150px] w-full" role="img" aria-label="Live download and upload traffic for the last 60 seconds" />
</template>
