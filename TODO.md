# VexLua Lua 5.1 Compatibility TODO

这份清单记录当前代码和原版 Lua 5.1 之间仍然能直接确认的缺失项与不一致项。

说明：

1. 这里优先记录语言、标准库、runtime 和 Lua 5.1 chunk 兼容层的剩余差异。
2. 已经在当前 diff 基线里闭合的 `luac.lua`、`trace-calls.lua`、`trace-globals.lua` 不再作为“当前缺口”重复记录。
3. 这里不展开 Lua C API/ABI 兼容目标，只记录当前 VexLua 这条实现路径上仍未对齐的 Lua 5.1 行为。
4. GC 调度策略、增量 step 算法以及 `setpause` / `setstepmul` 这类实现参数不再作为当前兼容目标；除非它们影响到明确的 Lua 语义可观察面，否则不写入这份 TODO。
5. `light userdata` 及其 metatable 不作为当前兼容目标；除非项目范围调整，否则不再把它们作为剩余缺口追踪。

标记说明：

1. [缺失] 当前基本没有实现。
2. [部分] 已有入口或子集，但还不是 Lua 5.1 完整语义。
3. [不一致] 接口存在，但行为和 Lua 5.1 不同。

当前状态：

1. 在当前兼容目标范围内，已知的 Lua 5.1 语言、标准库、runtime 与 chunk51 差分缺口已经清零。
2. `light userdata` 及其 metatable 仍明确不在当前兼容目标内；这不是“剩余 TODO”，而是范围外项。
3. 如后续出现新的可复现差分，再把它们按 `[缺失] / [部分] / [不一致]` 补回这份清单。
