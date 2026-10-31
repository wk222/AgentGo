//go:build web

// Command agentgo-web runs AgentGo without a native window: it serves the UI on
// a loopback port and opens it in the system browser. It is the Windows 7 build
// (no WebView2 needed) and is compiled with -tags web, which also leaves the
// Wails runtime out of the binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	frontendui "agentgo/frontend"
	"agentgo/internal/bridge"
	"agentgo/internal/hostui"
	"agentgo/internal/webshell"
)

const defaultAddr = "127.0.0.1:8788"

func main() {
	addr := flag.String("addr", defaultAddr, "loopback address to listen on; a free port is used if it is taken by something else")
	noBrowser := flag.Bool("no-browser", false, "do not open a browser window")
	remote := flag.Bool("remote", false, "let the phone app (and other devices) connect: listens on all interfaces and accepts the access key as X-Access-Key; put it behind Tailscale or HTTPS, never on the open internet")
	flag.Parse()

	if *remote {
		addrSet := false
		flag.Visit(func(f *flag.Flag) { addrSet = addrSet || f.Name == "addr" })
		if !addrSet {
			*addr = "0.0.0.0:8788"
		}
	}

	dataDir := bridge.DefaultDataDir()
	token, err := webshell.LoadOrCreateToken(dataDir)
	if err != nil {
		log.Fatalf("access token: %v", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		if webshell.IsRunning(*addr) {
			fmt.Println("AgentGo 已在运行:", *addr)
			if !*noBrowser {
				openBrowser("http://" + *addr + "/?t=" + token)
			}
			return
		}
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			log.Fatalf("listen: %v", err)
		}
	}

	rt, err := bridge.NewRuntime()
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	svc := bridge.NewAppService(rt)
	srv := webshell.New(webshell.Options{
		Service:        svc,
		Assets:         frontendui.Assets,
		AttachmentsDir: filepath.Join(rt.DataDir(), "attachments"),
		Token:          token,
		Remote:         *remote,
	})
	bridge.AddEventSink(srv.Publish)
	svc.StartWebEvents()

	pageURL := webshell.PageURL(ln.Addr(), token)
	fmt.Println("AgentGo 已启动:", "http://"+ln.Addr().String()+"/")
	if *remote {
		_, port, _ := net.SplitHostPort(ln.Addr().String())
		fmt.Println("远程模式已开启。手机 App 扫码或粘贴下面任一链接连接（链接里的密钥等同于控制这台电脑的权限，不要外传）：")
		links := webshell.ConnectLinks(port, token)
		for _, l := range links {
			fmt.Println("  ", l)
		}
		if len(links) > 0 {
			if qr, err := webshell.QRText(links[0]); err == nil {
				fmt.Println("\n扫描二维码连接（对应上面第一条链接）：")
				fmt.Println(qr)
			}
		}
	}
	fmt.Println("关闭本窗口即退出 AgentGo。")
	if !*noBrowser {
		openBrowser(pageURL)
	} else {
		fmt.Println("在浏览器中打开:", pageURL)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	if err := srv.Serve(ln); err != nil {
		log.Printf("serve: %v", err)
	}
}

func openBrowser(pageURL string) {
	b, err := hostui.OpenURL(pageURL)
	if err != nil {
		fmt.Println("无法自动打开浏览器，请手动打开:", pageURL, "(", err, ")")
		return
	}
	if b.Name == "default" {
		fmt.Println("已用系统默认浏览器打开。如果页面提示浏览器过旧，请安装 Chrome 109 或 Firefox 115 ESR。")
		return
	}
	fmt.Println("已用", b.Name, "打开")
}
