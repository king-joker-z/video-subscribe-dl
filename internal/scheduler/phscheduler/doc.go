// Package phscheduler 封装 Pornhub 专属的调度逻辑。
// PHScheduler 只负责 Pornhub 平台任务，有独立的暂停/冷却/进度推送机制，
// 不依赖 B 站 Downloader 的 SetExternalProgress/EmitEvent。
//
// 架构对齐 dscheduler（抖音调度器），每个平台独立调度，通过顶层 scheduler 统一编排。
package phscheduler
