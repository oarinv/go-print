package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	smbPort = 445             // SMB 协议默认端口
	timeout = 3 * time.Second // TCP 连接超时时间
	minHost = 100             // 起始主机号
	maxHost = 110             // 结束主机号
)

// 打印机信息结构体
type Printer struct {
	IP        string
	Name      string
	ShareName string
	FullPath  string
}

// 网络接口信息结构体
type InterfaceInfo struct {
	Name string
	IP   string
	CIDR string
}

// 获取本地启用的非回环 IPv4 网络接口信息
func getLocalInterfaces() ([]InterfaceInfo, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网络接口失败")
	}

	var result []InterfaceInfo
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				if strings.HasPrefix(ipNet.IP.String(), "169.254.") {
					continue
				}
				result = append(result, InterfaceInfo{
					Name: iface.Name,
					IP:   ipNet.IP.String(),
					CIDR: ipNet.String(),
				})
			}
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("未找到有效的 IPv4 地址")
	}
	return result, nil
}

// 默认选择第一个有效接口
func selectInterface(interfaces []InterfaceInfo) (InterfaceInfo, error) {
	if len(interfaces) > 0 {
		fmt.Printf("自动选择网络接口: %s (IP: %s)\n", interfaces[0].Name, interfaces[0].IP)
		return interfaces[0], nil
	}
	return InterfaceInfo{}, fmt.Errorf("没有可用的网络接口")
}

// 获取指定 CIDR 中的部分 IP 范围
func getNetworkRange(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	baseIP := ip.Mask(ipnet.Mask).To4()
	if baseIP == nil {
		return nil, fmt.Errorf("无效的 IPv4 网段")
	}

	var ips []string
	for i := minHost; i <= maxHost; i++ {
		candidate := net.IPv4(baseIP[0], baseIP[1], baseIP[2], byte(i))
		if ipnet.Contains(candidate) {
			ips = append(ips, candidate.String())
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("指定范围 (%d-%d) 内无可用 IP", minHost, maxHost)
	}
	return ips, nil
}

// 获取远程主机共享
func getShares(ip string) ([]string, error) {
	cmd := exec.Command("net", "view", fmt.Sprintf("\\\\%s", ip))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 net view 失败")
	}

	var shares []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Print") {
			printIndex := strings.Index(line, "Print")
			if printIndex > 0 {
				shareName := strings.TrimSpace(line[:printIndex])
				if shareName != "" && !strings.Contains(strings.ToLower(shareName), "ipc$") {
					shares = append(shares, shareName)
				}
			}
		}
	}

	if len(shares) == 0 {
		return nil, fmt.Errorf("未找到打印机共享")
	}
	return shares, nil
}

// 连接打印机（不验证是否已连接）
func connectPrinter(printer Printer) error {
	addCmd := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", "/in", "/n", printer.FullPath)
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("添加打印机失败: %v", err)
	}
	return nil
}

// 设置默认打印机（不验证）
func setDefaultPrinter(printer Printer) error {
	cmd := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", "/y", "/n", printer.FullPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("设置默认打印机失败: %v", err)
	}
	return nil
}

// 主程序入口
func main() {
	defer func() {
		fmt.Println("\n按回车键退出...")
		bufio.NewReader(os.Stdin).ReadBytes('\n')
	}()

	fmt.Println("开始扫描局域网中的共享打印机...")

	interfaces, err := getLocalInterfaces()
	if err != nil {
		fmt.Println("获取网络接口失败:", err)
		return
	}

	selectedInterface, err := selectInterface(interfaces)
	if err != nil {
		fmt.Println("选择接口失败:", err)
		return
	}

	ips, err := getNetworkRange(selectedInterface.CIDR)
	if err != nil {
		fmt.Println("获取网络范围失败:", err)
		return
	}

	var wg sync.WaitGroup
	printers := make(chan Printer, len(ips))

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, smbPort), timeout)
			if err != nil {
				return
			}
			conn.Close()

			shareNames, err := getShares(ip)
			if err != nil {
				return
			}

			for _, shareName := range shareNames {
				fullPath := fmt.Sprintf("\\\\%s\\%s", ip, shareName)
				printers <- Printer{
					IP:        ip,
					Name:      shareName,
					ShareName: shareName,
					FullPath:  fullPath,
				}
			}
		}(ip)
	}

	go func() {
		wg.Wait()
		close(printers)
	}()

	var targetPrinter *Printer
	for printer := range printers {
		fmt.Printf("发现打印机: %s (%s)\n", printer.FullPath, printer.IP)
		if targetPrinter == nil {
			targetPrinter = &printer
		}
	}

	if targetPrinter == nil {
		fmt.Println("未找到任何共享打印机")
		return
	}

	fmt.Printf("自动选择打印机: %s\n", targetPrinter.FullPath)

	if err := connectPrinter(*targetPrinter); err != nil {
		fmt.Println("打印机连接失败:", err)
		return
	}

	fmt.Println("正在设置默认打印机...")
	if err := setDefaultPrinter(*targetPrinter); err != nil {
		fmt.Println("设置默认打印机失败:", err)
		return
	}

	fmt.Printf("成功设置 %s 为默认打印机\n", targetPrinter.FullPath)
}
