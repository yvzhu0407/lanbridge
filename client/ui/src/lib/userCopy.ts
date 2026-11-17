export const userCopy = {
  guideSteps: [
    '房主在《文明6》里选「多人游戏 → 局域网 → 创建游戏」',
    '朋友在「局域网」列表里直接点你的房间',
    '看不到就点上面的「复制」把地址发给对方手动加',
  ],
  fallbackIssues: {
    node_down: {
      text: '后台服务没有响应',
      steps: ['打开「诊断」看最近事件', '重启 LANBridge', '还不行就重装'],
    },
    net_not_ready: {
      text: '你这台机器的虚拟网络还没就绪',
      steps: ['点「诊断」里的「重新检测」', '以管理员身份重启 LANBridge', '首次启动时请允许弹窗授权'],
    },
    planet_down: {
      text: '连不上房间服务器',
      steps: ['检查网络能否访问服务器', '稍等 1 分钟后重试'],
    },
  },
  chat: {
    notReadyHint: '等虚拟网络就绪后可以聊天',
    placeholder: '输入消息，回车发送…',
    noOthers: '房间内暂无其他在线可接收消息的成员',
  },
  summary: {
    notReady: '你这台机器没连上',
    allReady: '可以联机',
    probing: '正在检测…',
  },
}
