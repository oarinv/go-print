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
	// SMB 默认端口
	smbPort = 445
	// 超时设置
	timeout = 3 * time.Second
)

type Printer struct {
	IP      string
	Name    string
	Comment string
}

// InterfaceInfo 存储网络接口信息
type InterfaceInfo struct {
	Name string
	IP   string
	CIDR string
}

// getLocalInterfaces 获取所有网络接口的 IP 和 CIDR
func getLocalInterfaces() ([]InterfaceInfo, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("获取网络接口失败: %v", err)
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

// selectInterface 选择 WLAN 或以太网接口
func selectInterface(interfaces []InterfaceInfo) (InterfaceInfo, error) {
	for _, iface := range interfaces {
		nameLower := strings.ToLower(iface.Name)
		if strings.Contains(nameLower, "wlan") || strings.Contains(nameLower, "ethernet") || strings.Contains(nameLower, "eth") {
			return iface, nil
		}
	}

	fmt.Println("未找到 WLAN 或以太网接口，请手动选择：")
	for i, iface := range interfaces {
		fmt.Printf("[%d] 接口: %s, IP: %s, CIDR: %s\n", i, iface.Name, iface.IP, iface.CIDR)
	}

	var choice int
	fmt.Print("请输入接口编号 (0, 1, ...): ")
	_, err := fmt.Scan(&choice)
	if err != nil || choice < 0 || choice >= len(interfaces) {
		return InterfaceInfo{}, fmt.Errorf("无效的选择")
	}

	return interfaces[choice], nil
}

// scanNetwork 扫描局域网内的设备（限定为 192.168.0.100-110）
func scanNetwork(cidr string) ([]string, error) {
	// 忽略 CIDR，固定扫描 192.168.0.100-110
	var ips []string
	for i := 100; i <= 110; i++ {
		ips = append(ips, fmt.Sprintf("192.168.0.%d", i))
	}
	return ips, nil
}

// incIP 增加 IP 地址
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// getShares 使用 net view 命令获取目标设备的共享列表
func getShares(ip string) ([]string, error) {
	cmd := exec.Command("net", "view", fmt.Sprintf("\\\\%s", ip))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 net view \\\\%s 失败: %v, 输出: %s", ip, err, string(output))
	}

	var shares []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Print") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				shareName := fields[0]
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

// checkWMIC 检查 wmic 是否可用
func checkWMIC() error {
	cmd := exec.Command("wmic", "path", "win32_printer", "get", "Name")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("wmic 不可用: %v, 输出: %s", err, string(output))
	}
	return nil
}

// setDefaultPrinter 使用 wmic 或 PowerShell 设置默认打印机
func setDefaultPrinter(shareName string) error {
	// 优先尝试 wmic
	if err := checkWMIC(); err == nil {
		cmd := exec.Command("wmic", "printer", "where", fmt.Sprintf("Name='%s'", shareName), "call", "setdefaultprinter")
		output, err := cmd.CombinedOutput()
		if err == nil {
			fmt.Printf("成功使用 wmic 设置 %s 为默认打印机\n", shareName)
			return nil
		}
		fmt.Printf("wmic 设置默认打印机 %s 失败: %v, 输出: %s\n", shareName, err, string(output))
	} else {
		fmt.Printf("wmic 不可用: %v\n", err)
	}

	// 后备使用 PowerShell（Windows 11 支持）
	cmd := exec.Command("powershell", "-Command", fmt.Sprintf(`Set-Printer -Name "%s"`, shareName))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell 设置默认打印机 %s 失败: %v, 输出: %s", shareName, err, string(output))
	}
	fmt.Printf("成功使用 PowerShell 设置 %s 为默认打印机\n", shareName)
	return nil
}

func main() {
	fmt.Println("开始获取本地 IP 并扫描局域网中的共享打印机...")

	// 检查 wmic.exe 是否存在
	if _, err := os.Stat(`C:\Windows\System32\wbem\wmic.exe`); os.IsNotExist(err) {
		fmt.Println("警告：wmic.exe 不存在于 C:\\Windows\\System32\\wbem，请检查系统文件或 PATH 环境变量")
		fmt.Println("建议：运行 'sfc /scannow' 修复系统文件，或以管理员身份运行程序")
		fmt.Println("将依赖 PowerShell 设置默认打印机")
	}

	// 获取所有网络接口
	interfaces, err := getLocalInterfaces()
	if err != nil {
		fmt.Printf("获取网络接口失败: %v\n", err)
		return
	}

	// 选择接口
	selectedInterface, err := selectInterface(interfaces)
	if err != nil {
		fmt.Printf("选择接口失败: %v\n", err)
		return
	}
	fmt.Printf("使用接口: %s, IP: %s, CIDR: %s\n", selectedInterface.Name, selectedInterface.IP, selectedInterface.CIDR)

	// 验证 CIDR 是否为 192.168.0.0/24
	if !strings.HasPrefix(selectedInterface.CIDR, "192.168.0.") {
		fmt.Println("警告：选定的 CIDR 不是 192.168.0.0/24，是否继续？(y/n)")
		var response string
		fmt.Scan(&response)
		if strings.ToLower(response) != "y" {
			fmt.Println("程序退出")
			return
		}
	}

	// 获取局域网 IP 列表
	ips, err := scanNetwork(selectedInterface.CIDR)
	if err != nil {
		fmt.Printf("扫描网络失败: %v\n", err)
		return
	}

	var wg sync.WaitGroup
	printers := make(chan Printer, len(ips))

	// 并发扫描
	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			// 检查 TCP 连接
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, smbPort), timeout)
			if err != nil {
				fmt.Printf("IP %s: TCP 连接失败: %v\n", ip, err)
				return
			}
			conn.Close()

			// 获取共享列表
			shareNames, err := getShares(ip)
			if err != nil {
				fmt.Printf("IP %s: 获取共享列表失败: %v\n", ip, err)
				return
			}

			// 记录发现的打印机
			for _, shareName := range shareNames {
				fmt.Printf("IP %s: 发现打印机共享 %s\n", ip, shareName)
				printers <- Printer{
					IP:      ip,
					Name:    shareName,
					Comment: "Detected SMB Printer Share",
				}
			}
		}(ip)
	}

	// 等待所有扫描完成
	go func() {
		wg.Wait()
		close(printers)
	}()

	// 收集结果
	foundPrinters := []Printer{}
	for printer := range printers {
		foundPrinters = append(foundPrinters, printer)
	}

	// 输出结果
	if len(foundPrinters) == 0 {
		fmt.Println("未找到共享打印机")
		return
	}

	fmt.Println("发现以下共享打印机：")
	for _, printer := range foundPrinters {
		fmt.Printf("IP: %s, 名称: %s, 备注: %s\n", printer.IP, printer.Name, printer.Comment)
	}

	// 设置默认打印机
	for _, printer := range foundPrinters {
		if strings.Contains(strings.ToLower(printer.Name), "brother hl-2140 series") {
			err := setDefaultPrinter(printer.Name)
			if err != nil {
				fmt.Printf("设置默认打印机失败: %v\n", err)
			}
			break
		}
	}
}