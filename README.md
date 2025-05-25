# 局域网打印机自动连接工具

## 功能简介
- 自动扫描局域网内共享打印机
- 一键连接并设置为默认打印机
- 智能跳过已连接的打印机

## 系统要求
✅ Windows 7/10/11  
✅ 启用SMB协议  
✅ 管理员权限运行

## 快速开始
1. 下载`printer_connect.exe`
2. 右键选择"以管理员身份运行"
3. 等待程序自动完成所有操作

## 技术实现
```go
// 核心连接代码
func connectPrinter(printer Printer) error {
    cmd := exec.Command("rundll32.exe", "printui.dll,PrintUIEntry", 
        "/in", "/n", printer.FullPath)
    return cmd.Run()
}
```

## 协议许可
本项目基于 MIT License 开源发布。
