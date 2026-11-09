<script setup lang="ts">
import RoomSummary from './RoomSummary.vue'
import ConnectGuide from './ConnectGuide.vue'
import MemberList from './MemberList.vue'
import ChatPanel from './ChatPanel.vue'
</script>

<template>
  <div class="room-view-layout">
    <!-- 结论卡 + 我的联机地址 -->
    <RoomSummary />

    <!-- 主工作区：左侧 成员与指引 (1fr) + 右侧 聊天 (300px) -->
    <div class="room-main-grid">
      <div class="left-pane">
        <ConnectGuide />
        <MemberList />
      </div>

      <div class="right-pane">
        <ChatPanel />
      </div>
    </div>
  </div>
</template>

<style scoped>
.room-view-layout {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px;
  background: var(--bg);
  overflow: hidden;
}

.room-main-grid {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 12px;
}

.left-pane {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 0;
  overflow: hidden;
}

.right-pane {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}
</style>
