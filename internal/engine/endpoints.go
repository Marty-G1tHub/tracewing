package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Endpoint 是一个测速端点。端点表是**配置**，不是代码常量：
// 在数据目录放 endpoints.json（内容是 Endpoint 数组）即可整体替换内置表。
type Endpoint struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Region string `json:"region"` // cn | global（自定义区域名也可）
	Dir    string `json:"dir"`    // down | up
	URL    string `json:"url"`    // 下载 URL 可含 {bytes}
	Range  bool   `json:"range"`  // 下载是否用 HTTP Range
	Note   string `json:"note,omitempty"`
}

// 内置端点：每个 (区域, 方向) 按顺序探测，第一个可用的被选中；失效/被拦的会被探测自动跳过。
//
// 国内下行的选源原则（2026-10-27 在一条国内家宽上验证过可用，UA=tracewing/1.0；其他运营商 / 地区未验证）：
//  1. 多运营商商业 CDN 上的**大文件**优先——按用户所在运营商就近出节点，不偏袒某一家；
//  2. 文件名稳定的镜像（Arch 的 latest 链接不随版本轮换，Ubuntu 小版本号会轮换所以不用）；
//  3. 教育网镜像放在后面（对电信/移动用户可能绕路）；
//  4. speedtest.cn / Ookla 兼容的 /download?size= 节点放最后（单运营商节点，可能下线）。
//
// 每条的 Note 写明验证情况。**所有源都可能失效或被限速**，要改只需放 endpoints.json，不用改代码。
// 国内上行节点是上海电信 Ookla 兼容节点，**未在中国网络内验证**上行。
func BuiltinEndpoints() []Endpoint {
	arch := func(host string) string { return "https://" + host + "/archlinux/iso/latest/archlinux-x86_64.iso" }
	const verified = "2026-10-27 验证可用（Range 206）："
	return []Endpoint{
		{ID: "cn-down-tencent-cdn", Label: "腾讯 CDN（2GB 大文件）", Region: "cn", Dir: "down", Range: true,
			URL:  "https://dlied4.myapp.com/myapp/1104466820/cos.release-40109/10040714_com.tencent.tmgp.sgame_a2480356_8.2.1.9_F0BvnI.apk",
			Note: verified + "约 2.06GB，多运营商 CDN，无需 Referer"},
		{ID: "cn-down-huawei-arch", Label: "华为云镜像（CDN 加速）", Region: "cn", Dir: "down", URL: arch("repo.huaweicloud.com"), Range: true,
			Note: verified + "约 1.64GB，经 CDN，边缘按运营商分配"},
		{ID: "cn-down-xuexi-cdn", Label: "阿里 CDN（学习强国安装包）", Region: "cn", Dir: "down", Range: true,
			URL:  "https://wirelesscdn-download.xuexi.cn/publish/xuexi_android/latest/xuexi_android_10002068.apk",
			Note: verified + "约 280MB（偏小，Range 会回绕），阿里 CDN"},
		{ID: "cn-down-tencent-arch", Label: "腾讯云镜像", Region: "cn", Dir: "down", URL: arch("mirrors.cloud.tencent.com"), Range: true,
			Note: verified + "单连接首包后偏慢"},
		{ID: "cn-down-nju-arch", Label: "南京大学镜像", Region: "cn", Dir: "down", URL: arch("mirrors.nju.edu.cn"), Range: true,
			Note: verified + "教育网；Go 默认 UA 会被 429，需自定义 UA"},
		{ID: "cn-down-pku-arch", Label: "北京大学镜像", Region: "cn", Dir: "down", URL: arch("mirrors.pku.edu.cn"), Range: true,
			Note: verified + "教育网"},
		{ID: "cn-down-bfsu-arch", Label: "北外镜像", Region: "cn", Dir: "down", URL: arch("mirrors.bfsu.edu.cn"), Range: true,
			Note: verified + "教育网；Go 默认 UA / Mozilla 开头 UA 会被 403"},
		{ID: "cn-down-xjtu-arch", Label: "西安交大镜像", Region: "cn", Dir: "down", URL: arch("mirrors.xjtu.edu.cn"), Range: true,
			Note: verified + "教育网"},
		{ID: "cn-down-tuna-arch", Label: "清华镜像", Region: "cn", Dir: "down", URL: arch("mirrors.tuna.tsinghua.edu.cn"), Range: true,
			Note: verified + "Go 默认 UA / Mozilla 开头 UA 会被 403，且对异常高频 IP 封禁"},
		{ID: "cn-down-apple-cdn", Label: "苹果 CDN（国内节点，3GB）", Region: "cn", Dir: "down", Range: true,
			URL:  "http://updates-http.cdn-apple.com/2019WinterFCS/fullrestores/041-39257/32129B6C-292C-11E9-9E72-4511412B0A59/iPhone_4.7_12.1.4_16D57_Restore.ipsw",
			Note: verified + "约 3.1GB；仅 HTTP；旧固件文件，可能下架"},
		{ID: "cn-down-ustc-arch", Label: "中科大镜像", Region: "cn", Dir: "down", URL: arch("mirrors.ustc.edu.cn"), Range: true,
			Note: "对高频/重复请求会 403（Cloudflare 防护），测试中被拦；放后面作备用"},
		{ID: "cn-down-sptcn-cu", Label: "speedtest.cn 联通节点", Region: "cn", Dir: "down", Range: false,
			URL:  "https://node-218-26-162-114.speedtest.cn:51090/download?size={bytes}",
			Note: verified + "单运营商节点，对联通用户最友好；节点可能下线"},
		{ID: "cn-down-shtel-gen", Label: "上海电信 Ookla 兼容节点", Region: "cn", Dir: "down", Range: false,
			URL:  "http://speedtest1.online.sh.cn:8080/download?size={bytes}",
			Note: verified + "仅电信用户友好；节点可能下线"},
		{ID: "cn-down-hit-arch", Label: "哈工大镜像", Region: "cn", Dir: "down", URL: arch("mirrors.hit.edu.cn"), Range: true,
			Note: verified + "较慢，仅作最后备用"},

		{ID: "cn-up-shtel-1", Label: "上海电信 Ookla 兼容节点", Region: "cn", Dir: "up", URL: "http://speedtest1.online.sh.cn:8080/upload"},
		{ID: "cn-up-shtel-2", Label: "上海电信 Ookla 兼容节点(备用路径)", Region: "cn", Dir: "up", URL: "http://speedtest1.online.sh.cn:8080/speedtest/upload.php"},

		{ID: "gl-down-cf", Label: "Cloudflare", Region: "global", Dir: "down", URL: "https://speed.cloudflare.com/__down?bytes={bytes}"},
		{ID: "gl-down-ovh", Label: "OVH", Region: "global", Dir: "down", URL: "https://proof.ovh.net/files/1Gb.dat", Range: true},
		{ID: "gl-down-cachefly", Label: "CacheFly", Region: "global", Dir: "down", URL: "https://cachefly.cachefly.net/100mb.test", Range: true},
		{ID: "gl-up-cf", Label: "Cloudflare", Region: "global", Dir: "up", URL: "https://speed.cloudflare.com/__up"},
	}
}

// LoadEndpoints 读取 dir/endpoints.json；不存在则用内置表。第二个返回值是来源：builtin | file。
func LoadEndpoints(dir string) ([]Endpoint, string, error) {
	if dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, "endpoints.json")); err == nil {
			var eps []Endpoint
			if err := json.Unmarshal(b, &eps); err != nil {
				return BuiltinEndpoints(), "builtin", err
			}
			if len(eps) > 0 {
				return eps, "file", nil
			}
		}
	}
	return BuiltinEndpoints(), "builtin", nil
}

// Pick 返回某区域某方向的候选，保持配置顺序。
func Pick(eps []Endpoint, region, dir string) []Endpoint {
	var out []Endpoint
	for _, e := range eps {
		if e.Region == region && e.Dir == dir {
			out = append(out, e)
		}
	}
	return out
}
