# PC 显示与输入验收记录

## 已有证据

当前本地验收资料已覆盖：

- 1280×720 与多种窗口尺寸下的登录、OwnWorld、WorldVisit、Visitor、ConnectionLost 和 safe-return 页面。
- mouse/keyboard 的登录、文本输入、Tab 打开访问管理、按钮 disabled/loading/error、modal 与焦点恢复。
- 页面关闭、重连、Scene/HUD 重建及重复操作后无旧业务状态回写；业务闭环证据见 `.local/client-acceptance/20260720-authority-final/`、`20260720-commercial-final/` 与 `20260720-state-consistency-final/`。
- 自动 PlayMode 验收已在 `tasks.md` 的 3.6、7.3 和 8.6 记录，覆盖 Host teardown、输入 owner、focus、IME fixture 与销毁后回写保护。

## 最终人工验收

2026-07-20，操作者使用真实 Windows Development Player 完成任务 7.6，并确认以下组合全部通过：

- 16:9、16:10、21:9，以及窗口化和全屏显示。
- Windows DPI 缩放与页面布局、字体和关键内容可见性。
- mouse/keyboard/gamepad 的菜单打开、导航、确认、取消和 Gameplay 输入恢复。
- 文本输入与 Windows IME 的候选、提交、取消和非法 PlayerID 校验。
- modal/focus/cursor、loading/disabled/error 状态。
- 页面关闭、Scene/HUD 切换及销毁后的旧 callback 不回写。

该结论作为操作者人工验收事实记录；未提供的逐组合截图、设备型号或独立报告路径不作虚构。
