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

实现难度说明（按当前 VM / runtime / JIT 架构评估）：

1. [较易] 主要是补 runtime 或翻译层分支，不需要改值模型、调用协议或 upvalue 原语。
2. [中等] 需要跨 compiler / chunk51 / runtime 多处联动，但现有执行模型基本能承载。
3. [较难] 会碰 Value 编码、JIT 假设、upvalue 关闭粒度或多返回值调用协议，不能只在表层补接口。

## chunk51

- [ ] [部分] [较难] VexLua internal proto 导出到 Lua 5.1 chunk 时，`OpAppendTable`、`OpYield`、`OpIterPairs`、`OpIterIPairs` 等内部专用语义还没有翻译路径；这些不是简单一对一 opcode 映射，而是要重新降回 Lua 5.1 的 table / coroutine / generic for 协议。
- [ ] [部分] [中等] `OpLessEqualJump` 这类可以展开成更基础控制流的形态也还没覆盖；它不是最难的一档，但需要补完整的 chunk51 降级规则。
- [ ] [部分] [较难] Lua 5.1 chunk 导入侧仍有 opcode 空洞；`UNM` 相对直接，`TFORLOOP` / `SETLIST` 属于中等工程量，`CLOSE` 最难，因为当前 VM 只有整帧 `closeUpvalues`，没有按寄存器边界关闭 upvalue 的原语。
- [ ] [部分] [较难] 某些 call/tailcall 组合仍未覆盖，例如需要保留 pending multret 形态的 tailcall 分支；这会直接碰当前内部调用 / 返回协议，而不是单纯补一个翻译分支。
