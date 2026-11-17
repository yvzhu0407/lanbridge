<script setup lang="ts">
import { ref } from 'vue'
import { userCopy } from '../lib/userCopy'

const expanded = ref(false)
</script>

<template>
  <div class="connect-guide-wrap">
    <button class="guide-toggle-btn" @click="expanded = !expanded">
      <span class="guide-toggle-icon">{{ expanded ? '▾' : '▸' }}</span>
      <span class="guide-toggle-title">游戏里怎么连</span>
    </button>

    <ol v-show="expanded" class="guide-steps">
      <li v-for="(step, idx) in userCopy.guideSteps" :key="idx">
        <span class="step-num">{{ idx + 1 }}</span>
        <span class="step-text">{{ step }}</span>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.connect-guide-wrap {
  flex: none;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}

.guide-toggle-btn {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 6px;
  background: transparent;
  border: none;
  padding: 5px 10px;
  font-size: 11.5px;
  font-weight: 500;
  color: var(--fg-subtle);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s ease;
}

.guide-toggle-btn:hover {
  color: var(--fg);
  background: var(--card-hover);
}

.guide-toggle-icon {
  font-size: 10px;
  color: var(--fg-faint);
  width: 9px;
}

.guide-steps {
  list-style: none;
  padding: 4px 10px 8px 25px;
  border-top: 1px solid var(--border-subtle);
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.guide-steps li {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  font-size: 11.5px;
  color: var(--fg-muted);
  line-height: 1.45;
}

.step-num {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--border);
  color: var(--fg-muted);
  font-size: 9.5px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  margin-top: 1px;
}

.step-text { flex: 1; }
</style>
