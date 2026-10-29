// Package store 把每次测速落成一个文件夹（summary.json + report.html），
// 并维护一个轻量索引 history.jsonl 用于历史列表与非高峰基线。纯文本文件，随时可手工查看/删除。
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
)

type Store struct {
	Dir string
	mu  sync.Mutex
}

// Entry 是索引里的一行。
type Entry struct {
	ID       string             `json:"id"`
	Kind     string             `json:"kind"` // broadband | testip | vps
	At       string             `json:"at"`
	Title    string             `json:"title"`    // 列表标题
	Headline string             `json:"headline"` // 一句话结果
	Server   string             `json:"server,omitempty"`
	Source   string             `json:"source,omitempty"`
	Period   string             `json:"period,omitempty"`
	Med      map[string]float64 `json:"med,omitempty"` // VPS 各组吞吐中位，用于基线
}

// Resolve 选数据目录：显式指定 > exe 同目录下的“迹翼数据”（可写时）> 用户配置目录。
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		return explicit, os.MkdirAll(explicit, 0o755)
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Join(filepath.Dir(exe), "迹翼数据")
		if os.MkdirAll(d, 0o755) == nil && writable(d) {
			return d, nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "Tracewing")
	return d, os.MkdirAll(d, 0o755)
}

func writable(d string) bool {
	f := filepath.Join(d, ".w")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		return false
	}
	os.Remove(f)
	return true
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) runDir(id string) string { return filepath.Join(s.Dir, "runs", id) }

// ValidID 防路径穿越：只允许 [0-9a-z-]。
func ValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c == '-') {
			return false
		}
	}
	return true
}

func NewID(kind string, t time.Time) string { return t.Format("20060102-150405") + "-" + kind }

// Save 写 summary.json、report.html 并追加索引。
func (s *Store) Save(e Entry, summary any, reportHTML []byte) error {
	if !ValidID(e.ID) {
		return errors.New("bad id")
	}
	d := s.runDir(e.ID)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d, "summary.json"), b, 0o644); err != nil {
		return err
	}
	if len(reportHTML) > 0 {
		if err := os.WriteFile(filepath.Join(d, "report.html"), reportHTML, 0o644); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.Dir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := json.Marshal(e)
	_, err = f.Write(append(line, '\n'))
	return err
}

// List 返回全部索引（新的在前）。已被手工删除文件夹的条目会被过滤。
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(filepath.Join(s.Dir, "history.jsonl"))
	if err != nil {
		return []Entry{}
	}
	defer f.Close()
	out := []Entry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && ValidID(e.ID) {
			if _, err := os.Stat(s.runDir(e.ID)); err == nil {
				out = append(out, e)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

func (s *Store) Summary(id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, errors.New("bad id")
	}
	return os.ReadFile(filepath.Join(s.runDir(id), "summary.json"))
}

func (s *Store) Report(id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, errors.New("bad id")
	}
	return os.ReadFile(filepath.Join(s.runDir(id), "report.html"))
}

// ReportPath 返回离线报告文件的路径（文件必须存在）。
func (s *Store) ReportPath(id string) (string, error) {
	if !ValidID(id) {
		return "", errors.New("bad id")
	}
	p := filepath.Join(s.runDir(id), "report.html")
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

// Delete 删除一次记录并重写索引。
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return errors.New("bad id")
	}
	if err := os.RemoveAll(s.runDir(id)); err != nil {
		return err
	}
	// 索引按“文件夹是否存在”过滤，无需重写；compact 时再落盘
	s.compact()
	return nil
}

func (s *Store) compact() {
	es := s.List()
	s.mu.Lock()
	defer s.mu.Unlock()
	var sb strings.Builder
	for i := len(es) - 1; i >= 0; i-- {
		b, _ := json.Marshal(es[i])
		sb.Write(b)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(s.Dir, "history.jsonl"), []byte(sb.String()), 0o644)
}

// VPSHistory 同一服务器、同一来源的历史吞吐记录，供基线使用。
func (s *Store) VPSHistory(server, source string) []judge.HistRec {
	var out []judge.HistRec
	for _, e := range s.List() {
		if e.Kind == "vps" && e.Server == server && e.Source == source && len(e.Med) > 0 {
			out = append(out, judge.HistRec{Period: e.Period, Med: e.Med})
		}
	}
	return out
}

// Config 是用户上次的表单（不含令牌）。原样 JSON 存取。
func (s *Store) LoadConfig() json.RawMessage {
	b, err := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if err != nil || !json.Valid(b) {
		return json.RawMessage("{}")
	}
	return b
}

func (s *Store) SaveConfig(b []byte) error {
	if !json.Valid(b) || len(b) > 64<<10 {
		return errors.New("bad config")
	}
	return os.WriteFile(filepath.Join(s.Dir, "config.json"), b, 0o644)
}
