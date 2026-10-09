//go:build amd64

package raster

//go:noescape
func blendRow(c *blendVec, dst []byte)
