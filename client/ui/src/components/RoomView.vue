<script setup lang="ts">
import { state } from '../store'
import RoomSummary from './RoomSummary.vue'
import ConnectGuide from './ConnectGuide.vue'
import MemberList from './MemberList.vue'
import ChatPanel from './ChatPanel.vue'
</script>

<template>
  <div class="room-view-layout">
    <!-- 结论卡 + 联机地址 + 成员表，占满宽度 -->
    <RoomSummary />
    <ConnectGuide />
    <MemberList />

    <!-- 聊天：按需从右侧滑出，不常驻占位 -->
    <ChatPanel :key="state.currentRoom?.network_id" />
  </div>
</template>

<style scoped>
.room-view-layout {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  background: var(--bg);
  overflow: hidden;
  position: relative;
}
</style>
