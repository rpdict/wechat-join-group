package wechat

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Report struct {
	mu        sync.Mutex
	dir       string
	statePath string
	csvPath   string
	State     State
}

type State struct {
	Updated time.Time         `json:"updated"`
	Groups  map[string]Result `json:"groups"`
}

func LoadReport(dir string) *Report {
	r := &Report{
		dir:       dir,
		statePath: filepath.Join(dir, "state.json"),
		csvPath:   filepath.Join(dir, "result.csv"),
		State: State{
			Groups: make(map[string]Result),
		},
	}
	_ = os.MkdirAll(dir, 0o755)
	b, err := os.ReadFile(r.statePath)
	if err == nil {
		var st State
		if json.Unmarshal(b, &st) == nil {
			if st.Groups == nil {
				st.Groups = make(map[string]Result)
			}
			r.State = st
		}
	}
	return r
}

func (r *Report) ShouldSkip(group string) (Result, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.State.Groups[group]
	if !ok {
		return Result{}, false
	}
	if res.Status == "joined" || res.Status == "already_joined" {
		return res, true
	}
	return res, false
}

func (r *Report) MarkAlready(group, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	res, ok := r.State.Groups[group]
	if !ok {
		return fmt.Errorf("未找到群记录: %s", group)
	}
	res.Status = "already_joined"
	res.Reason = reason
	res.At = time.Now()
	r.State.Groups[group] = res
	r.State.Updated = time.Now()
	if err := r.saveStateLocked(); err != nil {
		return err
	}
	return r.writeCSVLocked()
}

func (r *Report) Add(res Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.State.Groups[res.Group] = res
	r.State.Updated = time.Now()
	if err := r.saveStateLocked(); err != nil {
		return err
	}
	if err := r.writeCSVLocked(); err != nil {
		return err
	}
	return nil
}

func (r *Report) saveStateLocked() error {
	b, err := json.MarshalIndent(r.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.statePath, b, 0o644)
}

func (r *Report) writeCSVLocked() error {
	groups := make([]Result, 0, len(r.State.Groups))
	for _, res := range r.State.Groups {
		groups = append(groups, res)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].At.Equal(groups[j].At) {
			return groups[i].Group < groups[j].Group
		}
		return groups[i].At.Before(groups[j].At)
	})

	f, err := os.OpenFile(r.csvPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	w := csv.NewWriter(f)
	if err := w.Write([]string{"群名", "状态", "原因", "邀请卡片", "时间"}); err != nil {
		return err
	}
	for _, res := range groups {
		record := []string{
			res.Group,
			res.Status,
			res.Reason,
			strings.ReplaceAll(res.Card, "\n", " "),
			res.At.Format("2006-01-02 15:04:05"),
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func (r *Report) WriteSummary() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.writeCSVLocked(); err != nil {
		return err
	}

	groups := make([]Result, 0, len(r.State.Groups))
	for _, res := range r.State.Groups {
		groups = append(groups, res)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Status == groups[j].Status {
			return groups[i].Group < groups[j].Group
		}
		return groups[i].Status < groups[j].Status
	})

	var joined, already, failed []Result
	for _, res := range groups {
		switch res.Status {
		case "joined":
			joined = append(joined, res)
		case "already_joined":
			already = append(already, res)
		default:
			failed = append(failed, res)
		}
	}

	var b strings.Builder
	b.WriteString("微信加群结果\n")
	b.WriteString("生成时间: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

	b.WriteString(fmt.Sprintf("本次已加入 (%d)\n", len(joined)))
	for _, res := range joined {
		b.WriteString("  - " + res.Group + "\n")
	}
	if len(joined) == 0 {
		b.WriteString("  （无）\n")
	}
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("之前已加入 / 已接受邀请 (%d)\n", len(already)))
	for _, res := range already {
		b.WriteString("  - " + res.Group + "\n")
	}
	if len(already) == 0 {
		b.WriteString("  （无）\n")
	}
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("未加入 / 失败 (%d)\n", len(failed)))
	for _, res := range failed {
		b.WriteString("  - " + res.Group + " | " + res.Reason + "\n")
	}
	if len(failed) == 0 {
		b.WriteString("  （无）\n")
	}

	summary := filepath.Join(r.dir, "not_joined.txt")
	return os.WriteFile(summary, append([]byte{0xEF, 0xBB, 0xBF}, []byte(b.String())...), 0o644)
}
