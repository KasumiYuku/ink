#include "textflag.h"

// func blendRow(c *blendVec, dst []byte)
// 半透明 alpha over, 每轮 16 字节(4 像素), 用 SSE2 —— amd64 的基线指令集, 不需要运行时探测。
// 常数布局: qS[16] rS[16] ia[8] 32897[8] 255[8] 1[8], 每段 16 字节。
// 算式: D = d*ia; qD = (D*32897)>>23; rD = D - qD*255
//       out = qS + qD + ((rS + rD + 1) >> 8)
// (x*32897)>>23 就是 x/255, 对 x∈[0,65535] 精确, 于是与标量的 `/255` 逐字节相同。
TEXT ·blendRow(SB), NOSPLIT, $0-32
	MOVQ c+0(FP), R8
	MOVQ dst_base+8(FP), DI
	MOVQ dst_len+16(FP), SI

	MOVOU 0(R8), X0           // qS 低 8 车道
	MOVOU 16(R8), X1          // qS 高 8 车道
	MOVOU 32(R8), X2          // rS 低
	MOVOU 48(R8), X3          // rS 高
	MOVOU 64(R8), X4          // ia
	MOVOU 80(R8), X5          // 32897
	MOVOU 96(R8), X6          // 255
	MOVOU 112(R8), X7         // 1
	PXOR  X15, X15

	XORQ CX, CX
loop:
	LEAQ 16(CX), R9
	CMPQ R9, SI
	JGT  done

	MOVOU (DI)(CX*1), X8
	MOVO  X8, X9
	PUNPCKLBW X15, X9         // 低 8 字节 -> 8 个 uint16
	MOVO  X8, X10
	PUNPCKHBW X15, X10        // 高 8 字节 -> 8 个 uint16

	// 低半
	MOVO    X9, X11
	PMULLW  X4, X11           // D
	MOVO    X11, X12
	PMULHUW X5, X12
	PSRLW   $7, X12           // qD
	MOVO    X12, X13
	PMULLW  X6, X13           // qD*255
	PSUBW   X13, X11          // rD
	PADDW   X2, X11           // + rS
	PADDW   X7, X11           // + 1
	PSRLW   $8, X11           // carry
	PADDW   X12, X11          // qD + carry
	PADDW   X0, X11           // + qS

	// 高半
	MOVO    X10, X12
	PMULLW  X4, X12
	MOVO    X12, X13
	PMULHUW X5, X13
	PSRLW   $7, X13
	MOVO    X13, X14
	PMULLW  X6, X14
	PSUBW   X14, X12
	PADDW   X3, X12
	PADDW   X7, X12
	PSRLW   $8, X12
	PADDW   X13, X12
	PADDW   X1, X12

	PACKUSWB X12, X11         // 收窄成 16 字节
	MOVOU    X11, (DI)(CX*1)

	ADDQ $16, CX
	JMP  loop
done:
	RET
