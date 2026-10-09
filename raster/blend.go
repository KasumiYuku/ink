package raster

// blendVec 是半透明 alpha-over 向量化的预计算常数, 两个架构共用同一份布局:
//
//	qS[16] rS[16] ia[8] 32897[8] 255[8] 1[8]
//
// 每段都是 16 字节。逐行复用, 所以只在整块填充开始时建一次。
type blendVec struct {
	q  [64]uint16
	cr uint32
	cg uint32
	cb uint32
	a  uint32
	ia uint32
}

// blendMinBytes 是走向量那条路的最小行长; 更短的跨段直接标量, 免得为几字节付调用开销。
const blendMinBytes = 64

// blendPrep 把一份墨色穿成向量常数。
func blendPrep(cr, cg, cb, a, ia uint32) blendVec {
	var v blendVec
	v.cr, v.cg, v.cb, v.a, v.ia = cr, cg, cb, a, ia
	src := [4]uint32{cr, cg, cb, a * 255}
	for i := 0; i < 16; i++ {
		x := src[i%4]
		v.q[i] = uint16(x / 255)
		v.q[16+i] = uint16(x % 255)
	}
	for i := 0; i < 8; i++ {
		v.q[32+i] = uint16(ia)
		v.q[40+i] = 32897
		v.q[48+i] = 255
		v.q[56+i] = 1
	}
	return v
}

// blendScalar 是那条内联展开的 alpha over; 与向量版逐字节相同, 也是短行的路。
func blendScalar(dst []byte, cr, cg, cb, a, ia uint32) {
	n := len(dst) &^ 3
	for x := 0; x < n; x += 4 {
		dst[x] = uint8((cr + uint32(dst[x])*ia) / 255)
		dst[x+1] = uint8((cg + uint32(dst[x+1])*ia) / 255)
		dst[x+2] = uint8((cb + uint32(dst[x+2])*ia) / 255)
		dst[x+3] = uint8((a*255 + uint32(dst[x+3])*ia) / 255)
	}
}
