# VexLua Lua 5.1 Compatibility TODO

这份清单记录当前代码和原版 Lua 5.1 之间仍然能直接确认的缺失项与不一致项。

说明：

1. 这里优先记录语言、标准库、runtime 和 Lua 5.1 chunk 兼容层的剩余差异。
2. 已经在当前 diff 基线里闭合的 `luac.lua`、`trace-calls.lua`、`trace-globals.lua` 不再作为“当前缺口”重复记录。
3. 这里不展开 Lua C API/ABI 兼容目标，只记录当前 VexLua 这条实现路径上仍未对齐的 Lua 5.1 行为。

标记说明：

1. [缺失] 当前基本没有实现。
2. [部分] 已有入口或子集，但还不是 Lua 5.1 完整语义。
3. [不一致] 接口存在，但行为和 Lua 5.1 不同。

## Base / package

- [ ] [缺失] 全局 `load` 仍未提供；当前只有 `loadstring`、`loadfile`、`dofile`。
- [ ] [缺失] `loadfile(nil)` 和 `dofile(nil)` 不支持 Lua 5.1 的 stdin 路径。
- [ ] [缺失] `package.path`、`package.cpath`、`package.config`、`package.loadlib` 仍未提供。
- [ ] [部分] `require` 当前默认只接了 `package.preload` searcher，没有 Lua 文件搜索器和 C 模块搜索器。

## debug / hook

- [ ] [缺失] `debug.getlocal`、`debug.setlocal`、`debug.gethook` 仍未实现。
- [ ] [部分] `debug.getinfo` 目前只覆盖 `f/l/n/S/u` 主路径，缺少 `L` / activelines 语义。
- [ ] [不一致] `debug.sethook` 虽然接受 `l` 和 count 参数，但 VM 里当前只有 call/return hook 真正分发，line/count hook 还没接通。

## error / fenv / GC 语义

- [ ] [不一致] `error(message, level)` 还没有 Lua 5.1 的 level 栈层级/报错位置修正语义。
- [ ] [不一致] `getfenv(number)` / `setfenv(number, env)` 当前没有实现 Lua 5.1 的栈层级规则；现在基本把 number 参数当成“当前环境”快捷路径处理。
- [ ] [不一致] `collectgarbage("step")` 当前直接跑整轮回收，不是 Lua 5.1 的增量 step 语义。
- [ ] [不一致] `collectgarbage("setpause")` / `collectgarbage("setstepmul")` 目前只是读写局部配置值，还没有接入实际 GC 调度。

## runtime 值模型 / metatable

- [ ] [缺失] runtime 值模型里还没有 `light userdata`。
- [ ] [缺失] `nil` metatable 还没有接入。
- [ ] [缺失] `light userdata` metatable 也还没有接入；这项依赖 light userdata 本身先落地。

## io / os

- [ ] [缺失] `io.popen` 仍然直接报不支持。
- [ ] [部分] `file:setvbuf` 目前只是参数校验后返回成功，没有真正实现 buffering mode / size 语义。
- [ ] [缺失] `os.exit` 仍然直接报不支持。
- [ ] [部分] `os.setlocale` 只覆盖 `C` / `POSIX` / 空字符串这类窄子集。
- [ ] [部分] `os.date` 目前只是手写的一组格式化分支，不是 Lua 5.1 那套完整的 `strftime` 兼容面。

## chunk51

- [ ] [部分] VexLua internal proto 导出到 Lua 5.1 chunk 时，仍只覆盖当前映射表里的 opcode；`OpAppendTable`、`OpYield`、`OpLessEqualJump`、`OpIterPairs`、`OpIterIPairs` 等形态会直接落到 `errLua51Unsupported`。
- [ ] [部分] Lua 5.1 chunk 导入侧仍有 opcode 空洞；`UNM`、`TFORLOOP`、`SETLIST`、`CLOSE` 等指令模式当前还没有翻译路径。
- [ ] [部分] 某些 call/tailcall 组合仍未覆盖，例如需要保留 pending multret 形态的 tailcall 分支仍会返回 unsupported。
