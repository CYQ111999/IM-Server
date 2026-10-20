#!/usr/bin/env bash

# Copyright © 2023 OpenIM. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# https://gist.github.com/cubxxw/28f997f2c9aff408630b072f010c1d64
#

set -e


############################## OpenIM 项目地址 ##############################
# …… 脚本其余内容 ……

# TODO
# 该脚本有三种配置方式。
# 1. 直接修改下方带注释的变量。
# 2. 通过 --help 等命令行参数传入。
# 3. 在外部设置变量，或通过环境变量传入。

# OpenIM 仓库的默认配置
# 可按需要自行调整 OpenIM 仓库相关设置。

# OpenIM 仓库所有者，默认为 "OpenIMSDK"；若使用其他所有者请相应替换。
OWNER="OpenIMSDK" 

# 仓库名称，默认为 "Open-IM-Server"；若使用其他仓库请相应替换。
REPO="Open-IM-Server" 

# 使用的 Go 版本，需与 OpenIM-Server 的要求兼容。
# 默认为 1.18，如需其他版本请相应替换。
GO_VERSION="1.20"

# 默认 HTTP_PORT 为 80；如需其他端口，取消注释并替换该值。
# HTTP_PORT=80

# 并发执行所用的 CPU 核心数，默认自动确定。
# 如需手动指定，取消下一行的注释。
# CPU=$(grep -c ^processor /proc/cpuinfo)

# 默认使用 OpenIM-Server release 中的最新 tag。
# 如需指定 tag，取消注释并把 "v3.0.0" 换成目标 tag。
# LATEST_TAG=v3.0.0

# 默认安装目录为 /tmp；如需其他目录，取消注释并替换 "/test"。
# DOWNLOAD_OPENIM_DIR="/test"

# GitHub 代理设置；如使用代理，取消注释并把空值替换为代理地址。
PROXY=

# 如有 GitHub token，把空值替换为你的 token。
GITHUB_TOKEN=

# 默认用户为 "root"；如需修改，取消注释并相应替换。
# OPENIM_USER=root 

# redis、mysql、mongo 的默认密码，同时也是 config/config.yaml 中的 accessSecret。
# 注意：密码需为 8 位以上的数字与字母组合；如需自定义，取消注释并替换。
# PASSWORD=openIM123

# minio 对外服务的默认 IP 与端口；如需其他地址，取消注释并替换。
# ENDPOINT=http://127.0.0.1:10005 

# 默认 API_URL，必要时替换。
# API_URL=http://127.0.0.1:10002/object/

# 默认数据目录；如需指定其他目录，取消注释并替换 "./"。
# DATA_DIR=./

############################## OpenIM 函数定义 ##############################
# 脚本安装概览
#
# 前置依赖：
#   - git
#   - make
#   - jq
#   - docker
#   - docker-compose
#   - go
#

# 检查脚本是否以 root 运行
function check_isroot() {
    if [ "$EUID" -ne 0 ]; then
    fatal "Please run the script as root or use sudo."
    fi
}

# 检查当前目录是否为 OpenIM 的 git 仓库
function check_git_repo() {
    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        # 位于 git 仓库内
        for remote in $(git remote); do
            repo_url=$(git remote get-url $remote)
            if [[ $repo_url == "https://github.com/openimsdk/open-im-server.git" || \
                  $repo_url == "https://github.com/openimsdk/open-im-server" || \
                  $repo_url == "git@github.com:openimsdk/open-im-server.git" ]]; then
                # 若是 OpenIMSDK 仓库
                info "Current directory is OpenIMSDK git repository."
                info "Executing installation directly."
                install_openim
                exit 0
            fi
            debug "Remote: $remote, URL: $repo_url"
        done
        # 若不是 OpenIMSDK 仓库
        debug "Current directory is not OpenIMSDK git repository."
    fi
    info "Current directory is not a git repository."
}

# 更新并安装所需工具的函数
function install_tools() {
    info "Checking and installing necessary tools, about git, make, jq, docker, docker-compose."
    local tools=("git" "make" "jq" "docker" "docker-compose")
    local install_cmd update_cmd os

    if grep -qEi "debian|buntu|mint" /etc/os-release; then
        os="Ubuntu"
        install_cmd="sudo apt install -y"
        update_cmd="sudo apt update"
    elif grep -qEi "fedora|rhel" /etc/os-release; then
        os="CentOS"
        install_cmd="sudo yum install -y"
        update_cmd="sudo yum update"
    else
        fatal "Unsupported OS, please use Ubuntu or CentOS."
    fi

    debug "Detected OS: $os"
    info "Updating system package repositories..."
    $update_cmd

    for tool in "${tools[@]}"; do
        if ! command -v $tool &> /dev/null; then
            warn "$tool is not installed. Installing now..."
            $install_cmd $tool
            success "$tool has been installed successfully."
        else
            info "$tool is already installed."
        fi
    done
}

# 检查是否已安装 Docker 与 Docker Compose 的函数
function check_docker() {
    if ! command -v docker &> /dev/null; then
        fatal "Docker is not installed. Please install Docker first."
    fi
    if ! command -v docker-compose &> /dev/null; then
        fatal "Docker Compose is not installed. Please install Docker Compose first."
    fi
}

# 未安装 Go 时，下载并安装 Go 的函数
function install_go() {
    command -v go >/dev/null 2>&1
    # 判断是否已定义 GO_VERSION
    if [ -z "$GO_VERSION" ]; then
        GO_VERSION="1.20"
    fi

    if [[ $? -ne 0 ]]; then
        warn "Go is not installed. Installing now..."
        curl -LO "https://golang.org/dl/go${GO_VERSION}.linux-amd64.tar.gz"
        if [ $? -ne 0 ]; then
            fatal "Download failed! Please check your network connectivity."
        fi
        sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
        echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc
        source ~/.bashrc
        success "Go has been installed successfully."
    else
        info "Go is already installed."
    fi
}

function download_source_code() {

    # 若函数外未定义 LATEST_TAG，在此获取，例如 v3.0.1-beta.1
    if [ -z "$LATEST_TAG" ]; then
        LATEST_TAG=$(curl -s "https://api.github.com/repos/$OWNER/$REPO/tags" | jq -r '.[0].name')
    fi

    # 若 LATEST_TAG 仍为空，设置默认值
    local DEFAULT_TAG="v3.0.0"

    LATEST_TAG="${LATEST_TAG:-$DEFAULT_TAG}"

    debug "DEFAULT_TAG: $DEFAULT_TAG"
    info "Use OpenIM Version LATEST_TAG: $LATEST_TAG"

    # 若函数外未定义 MODIFIED_TAG，在此处理，例如 3.0.1-beta.1
    if [ -z "$MODIFIED_TAG" ]; then
        MODIFIED_TAG=$(echo $LATEST_TAG | sed 's/v//')
    fi

    # 若 MODIFIED_TAG 仍为空，设置默认值
    local DEFAULT_MODIFIED_TAG="${DEFAULT_TAG#v}" 
    MODIFIED_TAG="${MODIFIED_TAG:-$DEFAULT_MODIFIED_TAG}"
    
    debug "MODIFIED_TAG: $MODIFIED_TAG"

    # 拼接 tar 包的下载地址
    TARBALL_URL="${PROXY}https://github.com/$OWNER/$REPO/archive/refs/tags/$LATEST_TAG.tar.gz"

    info "Downloaded OpenIM TARBALL_URL: $TARBALL_URL"

    info "Starting the OpenIM automated one-click deployment script."

    # 把下载与解压目录设为 /tmp
    if [ -z "$DOWNLOAD_OPENIM_DIR" ]; then
        DOWNLOAD_OPENIM_DIR="/tmp"
    fi

    # 检查 /tmp 目录是否存在
    if [ ! -d "$DOWNLOAD_OPENIM_DIR" ]; then
        warn "$DOWNLOAD_OPENIM_DIR does not exist. Creating it..."
        mkdir -p "$DOWNLOAD_OPENIM_DIR"
    fi

    info "Downloading OpenIM source code from $TARBALL_URL to $DOWNLOAD_OPENIM_DIR"
    
    curl -L -o "${DOWNLOAD_OPENIM_DIR}/${MODIFIED_TAG}.tar.gz" $TARBALL_URL

    tar -xzvf "${DOWNLOAD_OPENIM_DIR}/${MODIFIED_TAG}.tar.gz" -C "$DOWNLOAD_OPENIM_DIR"
    cd "$DOWNLOAD_OPENIM_DIR/$REPO-$MODIFIED_TAG"
    git init && git add . && git commit -m "init"  --no-verify

    success "Source code downloaded and extracted to $REPO-$MODIFIED_TAG"
}

function set_openim_env() {
    warn "This command can only be executed once. It will modify the component passwords in docker-compose based on the PASSWORD variable in .env, and modify the component passwords in config/config.yaml. If the password in .env changes, you need to first execute docker-compose down; rm components -rf and then execute this command."
    # 为各输入项设置默认值
    # 若未设置 OPENIM_USER 环境变量，默认为 'root'
    if [ -z "$OPENIM_USER" ]; then
        OPENIM_USER="root"
        debug "OPENIM_USER is not set. Defaulting to 'root'."
    fi

    # 若未设置 PASSWORD 环境变量，默认为 'openIM123'
    # 该密码用于 redis、mysql、mongo，以及 config/config.yaml 中的 accessSecret
    if [ -z "$PASSWORD" ]; then
        PASSWORD="openIM123"
        debug "PASSWORD is not set. Defaulting to 'openIM123'."
    fi

    # 若未设置 ENDPOINT 环境变量，默认为 'http://127.0.0.1:10005'
    # 这是 minio 对外服务的 IP 与端口，也可以是 storage.xx.xx 这类域名
    # 应用必须能访问该 IP 与端口（或域名）
    if [ -z "$ENDPOINT" ]; then
        ENDPOINT="http://127.0.0.1:10005"
        debug "ENDPOINT is not set. Defaulting to 'http://127.0.0.1:10005'."
    fi

    # 若未设置 API_URL 环境变量，默认为 'http://127.0.0.1:10002/object/'
    # 应用必须能访问该 IP 与端口（或域名）
    if [ -z "$API_URL" ]; then
        API_URL="http://127.0.0.1:10002/object/"
        debug "API_URL is not set. Defaulting to 'http://127.0.0.1:10002/object/'."
    fi

    # 若未设置 DATA_DIR 环境变量，默认为当前目录 './'
    # 可设为磁盘空间较大的目录
    if [ -z "$DATA_DIR" ]; then
        DATA_DIR="./"
        debug "DATA_DIR is not set. Defaulting to './'."
    fi
}

function install_openim() {
    info "Installing OpenIM"
    make -j${CPU} install V=1

    info "Checking installation"
    make check

    success "OpenIM installation completed successfully. Happy chatting!"
}

############################## OpenIM 帮助 ##############################

# 显示帮助信息的函数
function cmd_help() {
    openim_color
    color_echo ${BRIGHT_GREEN_PREFIX} "Usage: $0 [options]"
    color_echo ${BRIGHT_GREEN_PREFIX} "Options:"
    echo
    color_echo ${BLUE_PREFIX} "-i,  --install       ${CYAN_PREFIX}Execute the installation logic of the script${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-u,  --user          ${CYAN_PREFIX}set user (default: root)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-p,  --password      ${CYAN_PREFIX}set password (default: openIM123)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-e,  --endpoint      ${CYAN_PREFIX}set endpoint (default: http://127.0.0.1:10005)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-a,  --api           ${CYAN_PREFIX}set API URL (default: http://127.0.0.1:10002/object/)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-d,  --directory     ${CYAN_PREFIX}set directory for large disk space (default: ./)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-h,  --help          ${CYAN_PREFIX}display this help message and exit${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-cn, --china         ${CYAN_PREFIX}set to use the Chinese domestic proxy${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-t,  --tag           ${CYAN_PREFIX}specify the tag (default option, set to latest if not specified)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-r,  --release       ${CYAN_PREFIX}specify the release branch (cannot be used with the tag option)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-gt, --github-token  ${CYAN_PREFIX}set the GITHUB_TOKEN (default: not set)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "-g,  --go-version    ${CYAN_PREFIX}set the Go language version (default: GO_VERSION=\"1.20\")${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "--install-dir        ${CYAN_PREFIX}set the OpenIM installation directory (default: /tmp)${COLOR_SUFFIX}"
    color_echo ${BLUE_PREFIX} "--cpu                ${CYAN_PREFIX}set the number of concurrent processes${COLOR_SUFFIX}"
    echo
    color_echo ${RED_PREFIX} "Note: Only one of the -t/--tag or -r/--release options can be used at a time.${COLOR_SUFFIX}"
    color_echo ${RED_PREFIX} "If both are used or none of them are used, the -t/--tag option will be prioritized.${COLOR_SUFFIX}"
    echo
    exit 1
}

function parseinput() {
    # 设置默认值
    # OPENIM_USER=root
    # PASSWORD=openIM123
    # ENDPOINT=http://127.0.0.1:10005
    # API=http://127.0.0.1:10002/object/
    # DIRECTORY=./
    # CHINA=false
    # TAG=latest
    # RELEASE=""
    # GO_VERSION=1.20
    # INSTALL_DIR=/tmp
    # GITHUB_TOKEN=""
    # CPU=$(nproc)

    if [ $# -eq 0 ]; then
        cmd_help
        exit 1
    fi

    while [ $# -gt 0 ]; do
        case $1 in
            -h|--help)
                cmd_help
                exit
                ;;
            -u|--user)
                shift
                OPENIM_USER=$1
                ;;
            -p|--password)
                shift
                PASSWORD=$1
                ;;
            -e|--endpoint)
                shift
                ENDPOINT=$1
                ;;
            -a|--api)
                shift
                API=$1
                ;;
            -d|--directory)
                shift
                DIRECTORY=$1
                ;;
            -cn|--china)
                CHINA=true
                ;;
            -t|--tag)
                shift
                TAG=$1
                ;;
            -r|--release)
                shift
                RELEASE=$1
                ;;
            -g|--go-version)
                shift
                GO_VERSION=$1
                ;;
            --install-dir)
                shift
                INSTALL_DIR=$1
                ;;
            -gt|--github-token)
                shift
                GITHUB_TOKEN=$1
                ;;
            --cpu)
                shift
                CPU=$1
                ;;
            -i|--install)
                openim_main
                exit
                ;;
            *)
                echo "Unknown option: $1"
                cmd_help
                exit 1
                ;;
        esac
        shift
    done
}

############################## OpenIM 日志 ##############################
# 头部与 URL 使用青色文字
print_with_delay() {
  text="$1"
  delay="$2"

  for i in $(seq 0 $((${#text}-1))); do
    printf "${text:$i:1}"
    sleep $delay
  done
  printf "\n"
}

print_progress() {
  total="$1"
  delay="$2"

  printf "["
  for i in $(seq 1 $total); do
    printf "#"
    sleep $delay
  done
  printf "]\n"
}

# 彩色输出的函数
color_echo() {
    COLOR=$1
    shift
    echo -e "${COLOR} $* ${COLOR_SUFFIX}"
}

# 颜色定义
function openim_color() {
    COLOR_SUFFIX="\033[0m"      # 结束所有颜色与特殊效果

    BLACK_PREFIX="\033[30m"     # Black黑色前缀
    RED_PREFIX="\033[31m"       # Red红色前缀
    GREEN_PREFIX="\033[32m"     # Green绿色前缀
    YELLOW_PREFIX="\033[33m"    # Yellow黄色前缀
    BLUE_PREFIX="\033[34m"      # Blue蓝色前缀
    SKY_BLUE_PREFIX="\033[36m"  # Sky blue天蓝色前缀
    WHITE_PREFIX="\033[37m"     # White白色前缀
    BOLD_PREFIX="\033[1m"       # Bold加粗前缀
    UNDERLINE_PREFIX="\033[4m"  # Underline下划线前缀
    ITALIC_PREFIX="\033[3m"     # Italic斜体前缀
    BRIGHT_GREEN_PREFIX='\033[1;32m' # Bright green亮绿色前缀

    CYAN_PREFIX="\033[0;36m"     # Cyan青色前缀
}

# --- 日志相关的辅助函数 ---
info() {
    echo -e "[${GREEN_PREFIX}INFO${COLOR_SUFFIX}] " "$@"
}
warn() {
    echo -e "[${YELLOW_PREFIX}WARN${COLOR_SUFFIX}] " "$@" >&2
}
fatal() {
    echo -e "[${RED_PREFIX}ERROR${COLOR_SUFFIX}] " "$@" >&2
    exit 1
}
debug() {
    echo -e "[${BLUE_PREFIX}DEBUG${COLOR_SUFFIX}]===> " "$@"
}
success() {
    echo -e "${BRIGHT_GREEN_PREFIX}=== [SUCCESS] ===${COLOR_SUFFIX}\n=> " "$@"
}

function openim_logo() {
    # 头部与 URL 使用青色文字
    echo -e "\033[0;36m"

    # 显示 ASCII Art 图形 logo
    # 参考 http://patorjk.com/software/taag/#p=display&h=1&v=1&f=Doh&t=OpenIM
    print_with_delay '
                                                                                                                      
                                                                                                                      
     OOOOOOOOO                                                               IIIIIIIIIIMMMMMMMM               MMMMMMMM
   OO:::::::::OO                                                             I::::::::IM:::::::M             M:::::::M
 OO:::::::::::::OO                                                           I::::::::IM::::::::M           M::::::::M
O:::::::OOO:::::::O                                                          II::::::IIM:::::::::M         M:::::::::M
O::::::O   O::::::Oppppp   ppppppppp       eeeeeeeeeeee    nnnn  nnnnnnnn      I::::I  M::::::::::M       M::::::::::M
O:::::O     O:::::Op::::ppp:::::::::p    ee::::::::::::ee  n:::nn::::::::nn    I::::I  M:::::::::::M     M:::::::::::M
O:::::O     O:::::Op:::::::::::::::::p  e::::::eeeee:::::een::::::::::::::nn   I::::I  M:::::::M::::M   M::::M:::::::M
O:::::O     O:::::Opp::::::ppppp::::::pe::::::e     e:::::enn:::::::::::::::n  I::::I  M::::::M M::::M M::::M M::::::M
O:::::O     O:::::O p:::::p     p:::::pe:::::::eeeee::::::e  n:::::nnnn:::::n  I::::I  M::::::M  M::::M::::M  M::::::M
O:::::O     O:::::O p:::::p     p:::::pe:::::::::::::::::e   n::::n    n::::n  I::::I  M::::::M   M:::::::M   M::::::M
O:::::O     O:::::O p:::::p     p:::::pe::::::eeeeeeeeeee    n::::n    n::::n  I::::I  M::::::M    M:::::M    M::::::M
O::::::O   O::::::O p:::::p    p::::::pe:::::::e             n::::n    n::::n  I::::I  M::::::M     MMMMM     M::::::M
O:::::::OOO:::::::O p:::::ppppp:::::::pe::::::::e            n::::n    n::::nII::::::IIM::::::M               M::::::M
 OO:::::::::::::OO  p::::::::::::::::p  e::::::::eeeeeeee    n::::n    n::::nI::::::::IM::::::M               M::::::M
   OO:::::::::OO    p::::::::::::::pp    ee:::::::::::::e    n::::n    n::::nI::::::::IM::::::M               M::::::M
     OOOOOOOOO      p::::::pppppppp        eeeeeeeeeeeeee    nnnnnn    nnnnnnIIIIIIIIIIMMMMMMMM               MMMMMMMM
                    p:::::p                                                                                           
                    p:::::p                                                                                           
                   p:::::::p                                                                                          
                   p:::::::p                                                                                          
                   p:::::::p                                                                                          
                   ppppppppp                                                                                          
                                                                                                                      
    ' 0.0001

    # 显示产品地址
    print_with_delay "Discover more and contribute at: https://github.com/openimsdk/open-im-server" 0.01

    # 恢复默认文字颜色
    echo -e "\033[0m"

    # 产品描述使用绿色文字
    echo -e "\033[1;32m"

    print_with_delay "Open-IM-Server: Reinventing Instant Messaging" 0.01
    print_progress 50 0.02

    print_with_delay "Open-IM-Server is not just a product; it's a revolution. It's about bringing the power of seamless, real-time messaging to your fingertips. And it's about joining a global community of developers, dedicated to pushing the boundaries of what's possible." 0.01

    print_progress 50 0.02

    # 恢复默认文字颜色
    echo -e "\033[0m"

    # Slack 链接使用黄色文字
    echo -e "\033[1;33m"

    print_with_delay "Join our developer community on Slack: https://join.slack.com/t/openimsdk/shared_invite/zt-2ijy1ys1f-O0aEDCr7ExRZ7mwsHAVg9A" 0.01

    # 恢复默认文字颜色
    echo -e "\033[0m"
}

# 运行脚本的主函数
function openim_main() {
    check_git_repo
    check_isroot
    openim_color
    install_tools
    check_docker
    install_go
    download_source_code
    set_openim_env
    install_openim
    openim_logo

}

parseinput "$@"