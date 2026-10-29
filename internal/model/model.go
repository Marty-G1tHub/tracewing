// Package model 定义统一的结果结构（summary.json 的 schema）。
// 约定：缺数据用 null / 空，不编造数字。
package model

import (
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/route"
)

const SchemaVersion = "1.0"

// Meta 每份结果都带：用于复现。
type Meta struct {
	Kind       string `json:"kind"` // broadband | testip | vps
	Schema     string `json:"schema"`
	AppVersion string `json:"app_version"`
	ID         string `json:"id"`          // 目录名 yyyyMMdd-HHmmss-<kind>
	CapturedAt string `json:"captured_at"` // 本地时区 ISO8601
	Profile    string `json:"profile,omitempty"`
	OS         string `json:"os"`
}

// Note 是带级别的提示。Level: info | warn | bad。
type Note struct {
	Level string `json:"level"`
	Code  string `json:"code"`
	Text  string `json:"text"`
}

type Adapter struct {
	Name    string `json:"name"`
	Up      bool   `json:"up"`
	Suspect bool   `json:"suspect"` // 名称像 TUN/VPN（启发式）
}

type ExitInfo struct {
	Label   string `json:"label"`
	Via     string `json:"via"` // 访问回显端点时实际的路径
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Err     string `json:"err,omitempty"`
}

// Env 环境探测快照（只读探测，不改系统）。
type Env struct {
	Time        string             `json:"time"`
	PathMode    string             `json:"path_mode"`
	PathLabel   string             `json:"path_label"`
	SysProxy    netx.SysProxy      `json:"sys_proxy"`
	Adapters    []Adapter          `json:"adapters"`
	RouteSrc    string             `json:"route_src,omitempty"` // 去往公网时本机使用的源地址
	RouteIface  string             `json:"route_iface,omitempty"`
	TUN         bool               `json:"tun"`
	TUNReason   string             `json:"tun_reason,omitempty"`
	FakeIP      bool               `json:"fake_ip"`
	FakeIPHosts []netx.DNSEvidence `json:"fake_ip_hosts,omitempty"`
	ExitCN      *ExitInfo          `json:"exit_cn,omitempty"`
	ExitGlobal  *ExitInfo          `json:"exit_global,omitempty"`
	ExitDiffer  *bool              `json:"exit_differ,omitempty"` // 国内/境外两个回显的出口 IP 是否不同
	Notes       []Note             `json:"notes"`
}

// ---------- 宽带 ----------

type Round struct {
	N         int          `json:"n"`
	OK        bool         `json:"ok"`
	Mbps      float64      `json:"mbps"`
	MBps      float64      `json:"mb_per_s"`
	Bytes     int64        `json:"bytes"`      // 全程累计字节
	WindowSec float64      `json:"window_sec"` // 统计窗口（剔除预热）
	Streams   int          `json:"streams"`
	OKStreams int          `json:"ok_streams"`
	Err       string       `json:"err,omitempty"`
	Series    [][2]float64 `json:"series,omitempty"` // [t秒, 瞬时Mbps]
}

type Phase struct {
	Dir        string            `json:"dir"` // down | up
	EndpointID string            `json:"endpoint_id,omitempty"`
	URL        string            `json:"url,omitempty"`
	Via        string            `json:"via,omitempty"` // 实际路径
	DNS        *netx.DNSEvidence `json:"dns,omitempty"`
	Tried      []string          `json:"tried,omitempty"` // 探测过的源及结果
	Rounds     []Round           `json:"rounds"`
	Median     *float64          `json:"median_mbps"`
	MedianMBps *float64          `json:"median_mb_per_s"`
	Min        *float64          `json:"min_mbps"`
	Max        *float64          `json:"max_mbps"`
	PlanMbps   *float64          `json:"plan_mbps,omitempty"`
	UtilPct    *float64          `json:"plan_util_pct,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type Group struct {
	Region string    `json:"region"` // cn | global
	Label  string    `json:"label"`
	Exit   *ExitInfo `json:"exit,omitempty"`
	Down   *Phase    `json:"down,omitempty"`
	Up     *Phase    `json:"up,omitempty"`
}

type BBParams struct {
	Profile       string   `json:"profile"`
	Rounds        int      `json:"rounds"`
	DurationS     int      `json:"duration_s"`
	WarmupS       float64  `json:"warmup_s"`
	Streams       int      `json:"streams"`
	Regions       []string `json:"regions"`
	DoDown        bool     `json:"do_down"`
	DoUp          bool     `json:"do_up"`
	PlanDown      *float64 `json:"plan_down_mbps,omitempty"`
	PlanUp        *float64 `json:"plan_up_mbps,omitempty"`
	EndpointsFrom string   `json:"endpoints_from"` // builtin | file
}

type Broadband struct {
	Meta     Meta     `json:"meta"`
	Params   BBParams `json:"params"`
	Env      Env      `json:"env"`
	Groups   []Group  `json:"groups"`
	Verdict  string   `json:"verdict"`
	Warnings []string `json:"warnings,omitempty"`
}

// ---------- 测试 IP ----------

type Hop struct {
	TTL    int     `json:"ttl"`
	IP     string  `json:"ip,omitempty"`
	RTT    float64 `json:"rtt_ms"` // <0 表示无回应
	ASN    string  `json:"asn,omitempty"`
	Holder string  `json:"holder,omitempty"`
}

type IPTarget struct {
	IP        string    `json:"ip"`
	City      string    `json:"city,omitempty"`
	Region    string    `json:"region,omitempty"`
	Country   string    `json:"country,omitempty"`
	ASN       string    `json:"asn,omitempty"`
	Holder    string    `json:"holder,omitempty"`
	Sent      int       `json:"sent"`
	Recv      int       `json:"recv"`
	LossPct   float64   `json:"loss_pct"`
	P50       *float64  `json:"p50_ms"`
	P95       *float64  `json:"p95_ms"`
	Jitter    *float64  `json:"jitter_ms"` // P95 − P50（不是 RFC 抖动）
	Avg       *float64  `json:"avg_ms"`
	Min       *float64  `json:"min_ms"`
	Max       *float64  `json:"max_ms"`
	Stability string    `json:"stability,omitempty"`
	NoReply   bool      `json:"no_icmp_reply"`
	Samples   []float64 `json:"samples_ms"` // <0 表示丢失
	Hops      []Hop     `json:"hops,omitempty"`
	Reached   bool      `json:"endpoint_reached"`
	PathSum   string    `json:"path_summary,omitempty"`
	Err       string    `json:"err,omitempty"`
}

type IPParams struct {
	Method     string `json:"method"` // icmp | tcp
	TCPPort    int    `json:"tcp_port,omitempty"`
	Count      int    `json:"count"`
	TimeoutMs  int    `json:"timeout_ms"`
	IntervalMs int    `json:"interval_ms"`
	Hops       int    `json:"hops"`
	Trace      bool   `json:"trace"`
	Lookup     bool   `json:"lookup"`
}

type IPTest struct {
	Meta       Meta       `json:"meta"`
	Params     IPParams   `json:"params"`
	Env        Env        `json:"env"`
	Targets    []IPTarget `json:"targets"`
	Rejected   []string   `json:"rejected,omitempty"`
	TraceTrust bool       `json:"trace_trusted"`
	Verdict    []string   `json:"verdict"`
	Scope      string     `json:"scope"` // 未覆盖项声明
}

// ---------- VPS ----------

type Tput struct {
	Key     string   `json:"key"` // fwd1 rev1 fwd4 rev4
	Dir     string   `json:"dir"` // up(本机→服务器) | down
	Streams int      `json:"streams"`
	Rounds  []Round  `json:"rounds"`
	Min     *float64 `json:"min_mbps"`
	Median  *float64 `json:"median_mbps"`
	Max     *float64 `json:"max_mbps"`
	Error   string   `json:"error,omitempty"`
}

type Echo struct {
	Sent    int       `json:"sent"`
	Recv    int       `json:"recv"`
	Timeout float64   `json:"timeout_rate"`
	P50     *float64  `json:"p50_ms"`
	P95     *float64  `json:"p95_ms"`
	P99     *float64  `json:"p99_ms"`
	Jitter  *float64  `json:"jitter_ms"` // 相邻样本差的平均绝对值
	Min     *float64  `json:"min_ms"`
	Samples []float64 `json:"samples_ms"`
	What    string    `json:"what"` // 这个数字测的是什么
}

type UDPResult struct {
	Dir        string  `json:"dir"` // up | down
	TargetMbps float64 `json:"target_mbps"`
	Mbps       float64 `json:"recv_mbps"`
	Sent       int     `json:"sent"`
	Recv       int     `json:"recv"`
	LossPct    float64 `json:"loss_pct"`
	Reordered  int     `json:"reordered"`
	JitterMs   float64 `json:"jitter_ms"` // RFC3550 抖动
	Err        string  `json:"err,omitempty"`
}

type Resources struct {
	Available   bool     `json:"available"`
	Seconds     float64  `json:"seconds"`
	StealPct    *float64 `json:"cpu_steal_pct"`
	IdlePct     *float64 `json:"cpu_idle_pct"`
	BusyPct     *float64 `json:"cpu_busy_pct"`
	MemAvailPct *float64 `json:"mem_avail_pct"`
	Note        string   `json:"note,omitempty"`
}

type Diagnosis struct {
	Code            string   `json:"code"`
	Confidence      string   `json:"confidence"`
	Evidence        []string `json:"evidence"`
	CounterEvidence []string `json:"counter_evidence"`
	Limitations     []string `json:"limitations"`
}

type VPSParams struct {
	Source    string   `json:"source"`             // server | url | public
	Region    string   `json:"region,omitempty"`   // public: cn | global
	DownURL   string   `json:"down_url,omitempty"` // url 模式
	UpURL     string   `json:"up_url,omitempty"`   // url 模式
	Profile   string   `json:"profile"`
	DurationS int      `json:"duration_s"`
	Rounds    int      `json:"rounds"`
	GapS      int      `json:"gap_s"`
	Streams   int      `json:"streams"`
	EchoCount int      `json:"echo_count"`
	EchoMs    int      `json:"echo_interval_ms"`
	UploadCap *float64 `json:"upload_cap_mbps,omitempty"`
	UDPMbps   float64  `json:"udp_mbps,omitempty"`
}

type VPSTest struct {
	Meta        Meta        `json:"meta"`
	Params      VPSParams   `json:"params"`
	Server      string      `json:"server"` // 地址（不含令牌）
	ServerInfo  *ServerInfo `json:"server_info,omitempty"`
	Env         Env         `json:"env"`
	Via         string      `json:"via"`
	Period      string      `json:"period"` // peak | off_peak | normal
	Tput        []Tput      `json:"throughput"`
	ReverseGain *float64    `json:"reverse_gain,omitempty"`
	GainBand    string      `json:"reverse_gain_band,omitempty"`
	Util        *float64    `json:"forward_utilization,omitempty"`
	UtilBand    string      `json:"utilization_band,omitempty"`
	Echo        *Echo       `json:"echo,omitempty"`
	TCPConnect  *Echo       `json:"tcp_connect,omitempty"`
	UDP         []UDPResult `json:"udp,omitempty"`
	Resources   *Resources  `json:"resources,omitempty"`
	Baseline    *Baseline   `json:"baseline,omitempty"`
	Diagnosis   Diagnosis   `json:"diagnosis"`
	Status      string      `json:"status"` // PASS | PARTIAL | FAILED
	Notes       []Note      `json:"notes,omitempty"`
}

type ServerInfo struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	CPUs    int    `json:"cpus"`
	SeenAs  string `json:"client_seen_as,omitempty"` // 服务端看到的客户端出口 IP
}

type Baseline struct {
	N      int                `json:"offpeak_records"`
	Median map[string]float64 `json:"median_mbps"` // 每个 key 的非高峰中位
	Drop   map[string]float64 `json:"drop_pct,omitempty"`
}

// ---------- 线路（去程 / 回程） ----------

// RouteLine 是一条路径：原始跳数据 + 客户端给出的判定。
type RouteLine struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Carrier string         `json:"carrier,omitempty"` // telecom / unicom / mobile
	City    string         `json:"city,omitempty"`
	Trace   *route.Trace   `json:"trace,omitempty"`
	Verdict *route.Verdict `json:"verdict,omitempty"`
	Err     string         `json:"err,omitempty"`
}

type RouteParams struct {
	Outbound bool   `json:"outbound"`
	Return   bool   `json:"return"`
	Method   string `json:"local_method,omitempty"` // 本机探测方式
}

type RouteReport struct {
	Meta           Meta        `json:"meta"`
	Params         RouteParams `json:"params"`
	Server         string      `json:"server"` // 地址（不含令牌）
	ServerInfo     *ServerInfo `json:"server_info,omitempty"`
	AgentMethod    string      `json:"agent_method,omitempty"`
	PrefixVersion  string      `json:"prefix_version"`
	TargetsVersion string      `json:"targets_version"`
	Outbound       *RouteLine  `json:"outbound,omitempty"`
	Return         []RouteLine `json:"return,omitempty"`
	Client         *RouteLine  `json:"client_return,omitempty"` // agent → 你的出口 IP
	Headlines      []string    `json:"headlines"`
	Env            Env         `json:"env"`
	Notes          []Note      `json:"notes,omitempty"`
}
