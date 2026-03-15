package bilibili

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/url"
)

// addDmImgParams 添加B站反爬必需的 dm_img_* 参数
// 这些参数模拟浏览器 Canvas/WebGL 指纹，缺失会导致 -352 风控
// 参考: https://github.com/SocialSisterYi/bilibili-API-collect/issues/868
func addDmImgParams(params url.Values) {
	// dm_img_list: 模拟鼠标/触摸事件轨迹，空数组的 base64
	params.Set("dm_img_list", "[]")

	// dm_img_str: Canvas 指纹的某种变体编码
	params.Set("dm_img_str", generateDmImgStr())

	// dm_cover_img_str: WebGL 渲染指纹
	params.Set("dm_cover_img_str", generateDmCoverImgStr())

	// dm_img_inter: 交互数据
	params.Set("dm_img_inter", generateDmImgInter())
}

// generateDmImgStr 生成 dm_img_str 参数
// 使用真实浏览器中常见的 WebGL renderer 信息的 base64 编码
func generateDmImgStr() string {
	// "WebGL 1.0 (OpenGL ES 2.0 Chromium)" 的 base64
	return "V2ViR0wgMS4wIChPcGVuR0wgRVMgMi4wIENocm9taXVtKQ"
}

// generateDmCoverImgStr 生成 dm_cover_img_str 参数
// 使用真实浏览器中常见的 ANGLE renderer 信息的 base64 编码
func generateDmCoverImgStr() string {
	// "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)" 的 base64
	return "QU5HTEUgKEludGVsLCBJbnRlbChSKSBVSEQgR3JhcGhpY3MgNjMwIERpcmVjdDNEMTEgdnNfNV8wIHBzXzVfMCwgRDNEMTEp"
}

// generateDmImgInter 生成 dm_img_inter 参数
// 格式: {"ds":[],"wh":[W,H,N],"of":[X,Y,Z]}
func generateDmImgInter() string {
	w := 3 + rand.Intn(5)   // 3-7
	h := 2 + rand.Intn(4)   // 2-5
	n := 2 + rand.Intn(3)   // 2-4
	return fmt.Sprintf(`{"ds":[],"wh":[%d,%d,%d],"of":[%d,%d,%d]}`,
		w, h, n,
		rand.Intn(300)+100, rand.Intn(300)+100, rand.Intn(50))
}

// encodeDmBase64 base64 编码（URL safe, no padding）— 备用
func encodeDmBase64(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
