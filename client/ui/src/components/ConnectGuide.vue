<script setup lang="ts">
import { ref } from 'vue'
import { userCopy } from '../lib/userCopy'

const expanded = ref(false)
</script>

<template>
  <div class="connect-guide-wrap">
    <button class="guide-toggle-btn" @click="expanded = !expanded">
      <span class="guide-toggle-icon">{{ expanded ? '▾' : '▸' }}</span>
      <span class="guide-toggle-title">游戏内联机指引 (文明6)</span>
    </button>

    <div v-show="expanded" class="guide-content">
      <ol class="guide-steps">
        <li v-for="(step, idx) in userCopy.guideSteps" :key="idx" class="guide-step-item">
          <span class="step-num">{{ idx + 1 }}</span>
          <span class="step-text">{{ step }}</span>
        </li>
      </ol>
    </div>
  </div>
</template>

<style scoped>
.connect-guide-wrap {
  flex: none;
  background: var(--card-subtle);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius);
  overflow: hidden;
}

.guide-toggle-btn {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 8px;
  background: transparent;
  border: none;
  padding: 8px 12px;
  font-size: 12px;
  font-weight: 500;
  color: var(--fg-muted);
  cursor: pointer;
  text-align: left;
  transition: all 0.15s ease;
}

.guide-toggle-btn:hover {
  color: var(--fg);
  background: var(--card-hover);
}

.guide-toggle-icon {
  font-size: 11px;
  color: var(--fg-faint);
  display: inline-block;
  width: 10px;
}

.guide-content {
  padding: 8px 14px 12px 28px;
  border-top: 1px solid var(--border-subtle);
}

.guide-steps {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.guide-step-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: 12px;
  color: var(--fg-muted);
  line-height: 1.5;
}

.step-num {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--border);
  color: var(--fg);
  font-size: 10px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  margin-top: 1px;
}

.step-text {
  flex: 1;
}
</style>
