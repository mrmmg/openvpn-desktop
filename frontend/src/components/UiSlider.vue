<script setup lang="ts">
const props = defineProps<{ modelValue: number; min: number; max: number; label: string }>();
const emit = defineEmits<{ (e: "change", v: number): void }>();
import { ref, watch } from "vue";
const local = ref(props.modelValue);
watch(() => props.modelValue, (v) => (local.value = v));
</script>

<template>
  <div class="flex items-center gap-3">
    <input
      v-model.number="local"
      type="range"
      :min="min"
      :max="max"
      :aria-label="label"
      class="ui-range w-44"
      :style="{ '--p': ((local - min) / (max - min)) * 100 + '%' }"
      @change="emit('change', local)"
    />
    <span class="w-6 rounded-md bg-surface2 py-1 text-center text-[13px] font-medium tabular-nums">{{ local }}</span>
  </div>
</template>

<style scoped>
.ui-range {
  appearance: none;
  height: 6px;
  border-radius: 999px;
  background: linear-gradient(to right, var(--accent) var(--p), var(--line) var(--p));
}
.ui-range::-webkit-slider-thumb {
  appearance: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: #fff;
  border: 2px solid var(--accent);
  box-shadow: 0 1px 3px rgb(0 0 0 / 25%);
  cursor: pointer;
}
</style>
