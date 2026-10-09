//go:build !amd64 && !arm64

package raster

// blendRow 在没有向量实现的架构上就是标量那条路; 结果与向量版逐字节相同。
func blendRow(c *blendVec, dst []byte) {
	blendScalar(dst, c.cr, c.cg, c.cb, c.a, c.ia)
}
