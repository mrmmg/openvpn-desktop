<script setup lang="ts" generic="T extends string | number">
import { computed, nextTick, onBeforeUnmount, ref } from "vue";
import Icon from "./Icon.vue";

const props = defineProps<{
  modelValue: T;
  options: { value: T; label: string; hint?: string }[];
  label: string;
  width?: string;
}>();
const emit = defineEmits<{ (e: "update:modelValue", v: T): void }>();

const open = ref(false);
const active = ref(0);
const root = ref<HTMLElement>();
const list = ref<HTMLElement>();
const current = computed(() => props.options.find((o) => o.value === props.modelValue));
const listId = `sel-${Math.random().toString(36).slice(2)}`;

function outside(e: MouseEvent) {
  if (!root.value?.contains(e.target as Node)) close();
}
function show() {
  active.value = Math.max(0, props.options.findIndex((o) => o.value === props.modelValue));
  open.value = true;
  window.addEventListener("mousedown", outside);
  nextTick(() => list.value?.children[active.value]?.scrollIntoView({ block: "nearest" }));
}
function close() {
  open.value = false;
  window.removeEventListener("mousedown", outside);
}
function pick(i: number) {
  emit("update:modelValue", props.options[i].value);
  close();
  root.value?.querySelector("button")?.focus();
}
function key(e: KeyboardEvent) {
  if (!open.value) {
    if (["ArrowDown", "ArrowUp", "Enter", " "].includes(e.key)) {
      e.preventDefault();
      show();
    }
    return;
  }
  const n = props.options.length;
  if (e.key === "ArrowDown") active.value = (active.value + 1) % n;
  else if (e.key === "ArrowUp") active.value = (active.value + n - 1) % n;
  else if (e.key === "Home") active.value = 0;
  else if (e.key === "End") active.value = n - 1;
  else if (e.key === "Enter" || e.key === " ") pick(active.value);
  else if (e.key === "Escape" || e.key === "Tab") close();
  else return;
  e.preventDefault();
  nextTick(() => list.value?.children[active.value]?.scrollIntoView({ block: "nearest" }));
}
onBeforeUnmount(() => window.removeEventListener("mousedown", outside));
</script>

<template>
  <div ref="root" class="relative" :style="{ minWidth: width }">
    <button
      type="button"
      role="combobox"
      :aria-label="label"
      :aria-expanded="open"
      aria-haspopup="listbox"
      :aria-controls="listId"
      class="flex h-10 w-full items-center justify-between gap-2 rounded-lg border border-line bg-surface px-3 text-left text-[14px] transition-colors hover:border-muted/50"
      :class="open ? 'border-accent ring-2 ring-accent/20' : ''"
      @click="open ? close() : show()"
      @keydown="key"
    >
      <span class="truncate">{{ current?.label }}</span>
      <Icon name="chevron" :size="16" class="shrink-0 text-muted transition-transform" :class="open ? '-rotate-90' : 'rotate-90'" />
    </button>
    <ul
      v-if="open"
      :id="listId"
      ref="list"
      role="listbox"
      class="absolute left-0 right-0 z-30 mt-1 max-h-64 overflow-auto rounded-lg border border-line bg-surface py-1 shadow-card"
    >
      <li
        v-for="(o, i) in options"
        :key="String(o.value)"
        role="option"
        :aria-selected="o.value === modelValue"
        class="flex cursor-pointer items-center justify-between gap-3 px-3 py-2 text-[14px]"
        :class="[i === active ? 'bg-surface2' : '', o.value === modelValue ? 'font-medium text-accent-text' : '']"
        @mouseenter="active = i"
        @mousedown.prevent="pick(i)"
      >
        <span class="truncate">{{ o.label }}</span>
        <Icon v-if="o.value === modelValue" name="check" :size="16" class="shrink-0" />
      </li>
    </ul>
  </div>
</template>
