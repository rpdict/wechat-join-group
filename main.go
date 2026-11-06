package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"wechatJoinGroup/internal/wechat"
	"wechatJoinGroup/internal/win32"
)

func main() {
	chat := flag.String("chat", "XXXX", "要处理的聊天名称")
	sinceText := flag.String("since", "2026-10-21 11:55", "只处理该时间之后的邀请，格式 2006-01-02 15:04")
	outDir := flag.String("out", "out", "结果与调试截图输出目录")
	limit := flag.Int("limit", 0, "最多处理多少个邀请，0 表示不限制")
	dryRun := flag.Bool("dry-run", false, "只扫描并列出邀请，不执行加入")
	force := flag.Bool("force", false, "忽略已处理记录，重新处理所有邀请")
	oldest := flag.Bool("oldest", false, "按时间从旧到新处理（默认从新到旧）")
	screenMode := flag.Bool("screen", false, "使用屏幕识别模式（微信 4.2+ 不暴露 UIA，默认自动回退）")
	screenPages := flag.Int("pages", 20, "屏幕模式最多向上扫描的聊天页数")
	screenNoBottom := flag.Bool("no-bottom", false, "屏幕模式不先滚动到最底部，从当前聊天位置开始")
	markAlready := flag.String("mark-already", "", "将指定群标记为之前已加入（精确群名）")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	if *markAlready != "" {
		rep := wechat.LoadReport(*outDir)
		if err := rep.MarkAlready(*markAlready, "用户此前已手动加入"); err != nil {
			logger.Fatalf("标记失败: %v", err)
		}
		if err := rep.WriteSummary(); err != nil {
			logger.Fatalf("写总结失败: %v", err)
		}
		logger.Printf("已标记为之前已加入: %s", *markAlready)
		return
	}

	win32.InitDPI()
	logger.Printf("微信自动加群启动: chat=%q since=%s", *chat, *sinceText)

	var runner wechat.Runner
	uiaApp, uiaErr := wechat.New(*chat, *sinceText, *outDir, logger)
	if uiaErr == nil && !*screenMode {
		runner = uiaApp
	} else {
		if uiaApp != nil {
			uiaApp.Close()
		}
		if uiaErr != nil {
			logger.Printf("UIA 模式不可用: %v", uiaErr)
		}
		logger.Printf("使用屏幕识别模式：无法按时间过滤，改用卡片指纹去重")
		screenApp, err := wechat.NewScreen(*chat, *outDir, *screenPages, *screenNoBottom, logger)
		if err != nil {
			logger.Fatalf("初始化屏幕模式失败: %v", err)
		}
		runner = screenApp
	}
	defer runner.Close()

	if screenApp, ok := runner.(*wechat.ScreenApp); ok {
		joined, already, failed, skipped, runErr := screenApp.Run(*force, *limit, *oldest, *dryRun)
		if runErr != nil {
			logger.Fatalf("扫描邀请失败: %v", runErr)
		}
		if err := screenApp.WriteSummary(); err != nil {
			logger.Printf("写总结失败: %v", err)
		}
		logger.Printf("完成: 本次加入 %d，之前已加入 %d，未加入 %d，跳过 %d", joined, already, failed, skipped)
		logger.Printf("结果文件: %s", *outDir)
		fmt.Println("未加入清单: " + *outDir + string(os.PathSeparator) + "not_joined.txt")
		return
	}

	cards, err := runner.CollectCards()
	if err != nil {
		logger.Fatalf("扫描邀请失败: %v", err)
	}
	if len(cards) == 0 {
		logger.Printf("没有找到 %s 之后的群邀请", *sinceText)
		_ = runner.WriteSummary()
		return
	}

	logger.Printf("找到 %d 个 %s 之后的群邀请:", len(cards), *sinceText)
	for _, c := range cards {
		logger.Printf("  %2d. %s", c.Index, c.Group)
	}
	if *dryRun {
		logger.Printf("dry-run 模式，不执行加入")
		_ = runner.WriteSummary()
		return
	}

	if !*oldest {
		for i, j := 0, len(cards)-1; i < j; i, j = i+1, j-1 {
			cards[i], cards[j] = cards[j], cards[i]
		}
	}

	processed := 0
	joined, already, failed, skipped := 0, 0, 0, 0

	for i, c := range cards {
		if !*force {
			if prev, ok := runner.ShouldSkip(c); ok {
				logger.Printf("[%d/%d] %s 已有记录（%s），跳过", i+1, len(cards), c.Group, prev.Status)
				skipped++
				continue
			}
		}

		logger.Printf("[%d/%d] 处理: %s", i+1, len(cards), c.Group)
		res := runner.ProcessCard(c)
		if err := runner.AddResult(res); err != nil {
			logger.Printf("  保存结果失败: %v", err)
		}

		switch res.Status {
		case "joined":
			joined++
			logger.Printf("  -> 已加入")
		case "already_joined":
			already++
			logger.Printf("  -> 之前已加入（你已接受该邀请）")
		default:
			failed++
			logger.Printf("  -> 未加入: %s", res.Reason)
		}

		processed++
		if *limit > 0 && processed >= *limit {
			logger.Printf("已达到 -limit %d，停止", *limit)
			break
		}
		time.Sleep(700 * time.Millisecond)
	}

	if err := runner.WriteSummary(); err != nil {
		logger.Printf("写总结失败: %v", err)
	}

	logger.Printf("完成: 本次加入 %d，之前已加入 %d，未加入 %d，跳过 %d", joined, already, failed, skipped)
	logger.Printf("结果文件: %s", *outDir)
	fmt.Println("未加入清单: " + *outDir + string(os.PathSeparator) + "not_joined.txt")
}
