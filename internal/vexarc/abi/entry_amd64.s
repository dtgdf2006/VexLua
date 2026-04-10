#include "textflag.h"

TEXT ·enterCompiled(SB), NOSPLIT, $0-64
	PUSHQ R12
	PUSHQ R13
	PUSHQ R14
	PUSHQ R15

	MOVQ entry+0(FP), AX
	MOVQ heapBase+8(FP), R15
	MOVQ vmState+16(FP), R14
	MOVQ frame+24(FP), R13
	MOVQ regsBase+32(FP), R12
	MOVQ execCtx+40(FP), R11
	CALL AX

	MOVQ AX, status+48(FP)
	MOVQ DX, aux+56(FP)

	POPQ R15
	POPQ R14
	POPQ R13
	POPQ R12
	RET
