#include "textflag.h"

// func blendRow(c *blendVec, dst []byte)
// 半透明 alpha over, 每轮 16 字节(4 像素), 用 NEON —— arm64 的基线指令集(ARMv8-A 强制 asimd)。
// 常数布局: qS[16] rS[16] ia[8] 32897[8] 255[8] 1[8], 每段 16 字节。
// 算式与标量逐字节相同: D = d*ia; qD = (D*32897)>>23; rD = D - qD*255
//                      out = qS + qD + ((rS + rD + 1) >> 8)
TEXT ·blendRow(SB), NOSPLIT, $0-32
	MOVD c+0(FP), R2
	MOVD dst_base+8(FP), R0
	MOVD dst_len+16(FP), R1

	VLD1 (R2), [V0.H8]        // qS 低
	ADD  $16, R2
	VLD1 (R2), [V1.H8]        // qS 高
	ADD  $16, R2
	VLD1 (R2), [V2.H8]        // rS 低
	ADD  $16, R2
	VLD1 (R2), [V3.H8]        // rS 高
	ADD  $16, R2
	VLD1 (R2), [V4.H8]        // ia
	ADD  $16, R2
	VLD1 (R2), [V5.H8]        // 32897
	ADD  $16, R2
	VLD1 (R2), [V6.H8]        // 255
	ADD  $16, R2
	VLD1 (R2), [V7.H8]        // 1

	MOVD $0, R3
loop:
	ADD  $16, R3, R4
	CMP  R4, R1
	BLT  done

	ADD  R0, R3, R5
	VLD1 (R5), [V8.B16]

	VUSHLL  $0, V8.B8, V10.H8    // 低 8 字节 -> 8 个 uint16
	VUSHLL2 $0, V8.B16, V11.H8   // 高 8 字节 -> 8 个 uint16

	// 低半
	VMUL    V10.H8, V4.H8, V12.H8    // D
	VUMULL  V12.H4, V5.H4, V14.S4    // 低 4 车道的 32 位积
	VUMULL2 V12.H8, V5.H8, V16.S4    // 高 4 车道
	VUSHR   $23, V14.S4, V14.S4
	VUSHR   $23, V16.S4, V16.S4
	VXTN    V14.S4, V18.H4           // qD
	VXTN2   V16.S4, V18.H8
	VMUL    V18.H8, V6.H8, V20.H8    // qD*255
	VSUB    V20.H8, V12.H8, V22.H8   // rD = D - qD*255
	VADD    V22.H8, V2.H8, V22.H8    // + rS
	VADD    V22.H8, V7.H8, V22.H8    // + 1
	VUSHR   $8, V22.H8, V22.H8       // carry
	VADD    V18.H8, V0.H8, V18.H8    // qD + qS
	VADD    V18.H8, V22.H8, V18.H8
	VXTN    V18.H8, V24.B8

	// 高半
	VMUL    V11.H8, V4.H8, V13.H8
	VUMULL  V13.H4, V5.H4, V15.S4
	VUMULL2 V13.H8, V5.H8, V17.S4
	VUSHR   $23, V15.S4, V15.S4
	VUSHR   $23, V17.S4, V17.S4
	VXTN    V15.S4, V19.H4
	VXTN2   V17.S4, V19.H8
	VMUL    V19.H8, V6.H8, V21.H8
	VSUB    V21.H8, V13.H8, V23.H8
	VADD    V23.H8, V3.H8, V23.H8
	VADD    V23.H8, V7.H8, V23.H8
	VUSHR   $8, V23.H8, V23.H8
	VADD    V19.H8, V1.H8, V19.H8
	VADD    V19.H8, V23.H8, V19.H8
	VXTN2   V19.H8, V24.B16          // 收窄并接到低半后面

	VST1 [V24.B16], (R5)

	ADD $16, R3, R3
	B   loop
done:
	RET
