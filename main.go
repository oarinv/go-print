package main

import (
	"bufio"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	smbPort = 445
	timeout = 3 * time.Second
)

type Printer struct {
	IP        string
	Name      string
	ShareName string
	FullPath  string
}

type InterfaceInfo struct {
	Name string
	IP   string
	CIDR string
}

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

func selectInterface(interfaces []InterfaceInfo) (InterfaceInfo, error) {
	if len(interfaces) > 0 {
		fmt.Printf("自动选择网络接口: %s (IP: %s)\n", interfaces[0].Name, interfaces[0].IP)
		return interfaces[0], nil
	}
	return InterfaceInfo{}, fmt.Errorf("没有可用的网络接口")
}

func getNetworkRange(cidr string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	if len(ips) > 2 {
		return ips[1 : len(ips)-1], nil
	}
	return ips, nil
}

func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

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

func isPrinterConnected(printerName string) bool {
	cmd := exec.Command("powershell", "-Command", fmt.Sprintf(`Get-Printer -Name "%s" -ErrorAction SilentlyContinue | Measure-Object | Select-Object -ExpandProperty Count`, printerName))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "1"
}

func connectPrinter(printer Printer) error {
	if isPrinterConnected(printer.FullPath) {
		fmt.Printf("打印机 %s 已连接，跳过连接步骤\n", printer.FullPath)
		return nil
	}

	// 添加网络打印机
	addCmd := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", "/in", "/n", printer.FullPath)
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("添加打印机失败: %v", err)
	}

	return nil
}

func setDefaultPrinter(printer Printer) error {
	cmd := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", "/y", "/n", printer.FullPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("设置默认打印机失败: %v", err)
	}

	// 验证是否设置成功
	checkCmd := exec.Command("powershell", "-Command", fmt.Sprintf(`(Get-Printer -Name "%s").IsDefault`, printer.FullPath))
	output, err := checkCmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "True") {
		return fmt.Errorf("验证默认打印机设置失败")
	}

	return nil
}

func main() {
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
