<script setup lang="ts">
import Icon from "./Icon.vue";

withDefaults(
  defineProps<{ icon?: string; title: string; description?: string; compact?: boolean }>(),
  { icon: "database", compact: false },
);
</script>

<template>
  <div class="empty" :style="compact ? 'padding: 16px 12px; min-height: 0' : ''">
    <div class="empty-icon" :style="compact ? 'width:34px;height:34px;border-radius:10px' : ''">
      <Icon :name="icon" :size="compact ? 18 : 24" />
    </div>
    <h3 :style="compact ? 'font-size:12px' : ''">{{ title }}</h3>
    <p v-if="description">{{ description }}</p>
    <div v-if="$slots.default" class="empty-actions"><slot /></div>
  </div>
</template>
