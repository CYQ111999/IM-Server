// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

var (
	// 默认模板版本。
	defaultTemplateVersion = "v1.3.0"
)

func main() {
	// 定义获取最新版本号的 URL
	// latestVersionURL := "https://github.com/openimsdk/chat/releases/latest"
	// latestVersion, err := getLatestVersion(latestVersionURL)
	// if err != nil {
	// 	fmt.Printf("Failed to get the latest version: %v\n", err)
	// 	return
	// }
	latestVersion := defaultTemplateVersion

	// getLatestVersion

	// 拼接下载 URL
	downloadURL := fmt.Sprintf("https://github.com/openimsdk/chat/releases/download/%s/chat_Linux_x86_64.tar.gz", latestVersion)

	// 设置安装目录
	installDir := "/tmp/chat"

	// 执行前先清空安装目录
	err := os.RemoveAll(installDir)
	if err != nil {
		fmt.Printf("Failed to clear installation directory: %v\n", err)
		return
	}

	// 创建安装目录
	err = os.MkdirAll(installDir, 0755)
	if err != nil {
		fmt.Printf("Failed to create installation directory: %v\n", err)
		return
	}

	// 下载 OpenIM Chat 并解压到安装目录
	err = downloadAndExtract(downloadURL, installDir)
	if err != nil {
		fmt.Printf("Failed to download and extract OpenIM Chat: %v\n", err)
		return
	}

	// 创建配置文件目录
	configDir := filepath.Join(installDir, "config")
	err = os.MkdirAll(configDir, 0755)
	if err != nil {
		fmt.Printf("Failed to create configuration directory: %v\n", err)
		return
	}

	// 下载配置文件
	configURL := "https://raw.githubusercontent.com/openimsdk/chat/main/config/config.yaml"
	err = downloadAndExtract(configURL, configDir)
	if err != nil {
		fmt.Printf("Failed to download and extract configuration files: %v\n", err)
		return
	}

	// 定义需要启动的进程
	cmds := []string{
		"admin-api",
		"admin-rpc",
		"chat-api",
		"chat-rpc",
	}

	// 每个进程在独立的 goroutine 中启动
	for _, cmd := range cmds {
		go startProcess(filepath.Join(installDir, cmd))
	}

	// 永久阻塞主线程
	select {}
}

/* func getLatestVersion(url string) (string, error) {
	resp, err := webhook.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

// 	location := resp.Header.Get("Location")
// 	if location == "" {
// 		return defaultTemplateVersion, nil
// 	}

	// 从 URL 中解析版本号
	latestVersion := filepath.Base(location)
	return latestVersion, nil
} */

// downloadAndExtract 从 URL 下载文件并解压到目标目录。
func downloadAndExtract(url, destDir string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("error downloading file, HTTP status code: %d", resp.StatusCode)
	}

	// 创建目标目录
	err = os.MkdirAll(destDir, 0755)
	if err != nil {
		return err
	}

	// Define the path for the downloaded file
	filePath := filepath.Join(destDir, "downloaded_file.tar.gz")
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 复制已下载的文件
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return err
	}

	// 解压文件
	cmd := exec.Command("tar", "xzvf", filePath, "-C", destDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// startProcess 启动进程并输出遇到的错误。
func startProcess(cmdPath string) {
	cmd := exec.Command(cmdPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Failed to start process %s: %v\n", cmdPath, err)
	}
}
