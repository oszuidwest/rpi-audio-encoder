#!/usr/bin/env bash
set -euo pipefail

# Configuration
GITHUB_REPO="oszuidwest/zwfm-encoder"
ENCODER_SERVICE_URL="https://raw.githubusercontent.com/${GITHUB_REPO}/main/deploy/encoder.service"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/encoder"
SERVICE_PATH="/etc/systemd/system/encoder.service"

# Functions library
FUNCTIONS_LIB_PATH=$(mktemp)
FUNCTIONS_LIB_URL="https://raw.githubusercontent.com/oszuidwest/bash-functions/main/common-functions.sh"

# Clean up temporary file on exit
trap 'rm -f "$FUNCTIONS_LIB_PATH"' EXIT

# General Raspberry Pi configuration
CONFIG_FILE_PATHS=("/boot/firmware/config.txt" "/boot/config.txt")
FIRST_IP=$(hostname -I | awk '{print $1}')

# Start with a clean terminal
clear

# Download the functions library
if ! curl -s -o "$FUNCTIONS_LIB_PATH" "$FUNCTIONS_LIB_URL"; then
  echo -e "*** Failed to download functions library. Please check your network connection! ***"
  exit 1
fi

# Source the functions file
# shellcheck source=/dev/null
source "$FUNCTIONS_LIB_PATH"

# Set color variables and perform initial checks
set_colors
check_user_privileges privileged
is_this_linux
is_this_os_64bit
check_rpi_model 4

# Determine the correct config file path
CONFIG_FILE=""
for path in "${CONFIG_FILE_PATHS[@]}"; do
  if [ -f "$path" ]; then
    CONFIG_FILE="$path"
    break
  fi
done

if [ -z "$CONFIG_FILE" ]; then
  echo -e "${RED}Error: config.txt not found in known locations.${NC}"
  exit 1
fi

# Check if the required tools are installed
require_tool curl systemctl

# Banner
cat << "EOF"
 ______     _     ___          __       _     ______ __  __
|___  /    (_)   | \ \        / /      | |   |  ____|  \/  |
   / /_   _ _  __| |\ \  /\  / /__  ___| |_  | |__  | \  / |
  / /| | | | |/ _` | \ \/  \/ / _ \/ __| __| |  __| | |\/| |
 / /_| |_| | | (_| |  \  /\  /  __/\__ \ |_  | |    | |  | |
/_____\__,_|_|\__,_|   \/  \/ \___||___/\__| |_|    |_|  |_|
EOF

# Greeting
echo -e "${GREEN}⎎ Audio encoder set-up for Raspberry Pi${NC}\n"

# Check if the HiFiBerry is configured
if ! grep -q "^dtoverlay=hifiberry" "$CONFIG_FILE"; then
  echo -e "${RED}No HiFiBerry card configured in the $CONFIG_FILE file. Exiting...${NC}\n" >&2
  exit 1
fi

# Ask for OS updates
ask_user "DO_UPDATES" "y" "Do you want to perform all OS updates? (y/n)" "y/n"

# Timezone configuration
set_timezone Europe/Amsterdam

# Run OS updates if requested
if [ "$DO_UPDATES" == "y" ]; then
  update_os silent
fi

# Install dependencies
echo -e "${BLUE}►► Installing FFmpeg and alsa-utils...${NC}"
install_packages silent ffmpeg alsa-utils

# Stop existing service if running
if systemctl is-active --quiet encoder 2>/dev/null; then
  echo -e "${BLUE}►► Stopping existing encoder service...${NC}"
  systemctl stop encoder
fi

# Get latest release version from GitHub API
echo -e "${BLUE}►► Fetching latest release information...${NC}"
LATEST_RELEASE=$(curl -s "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
if [ -z "$LATEST_RELEASE" ]; then
  echo -e "${RED}Failed to fetch latest release version${NC}"
  exit 1
fi
echo -e "${GREEN}✓ Latest version: ${LATEST_RELEASE}${NC}"

# Download encoder binary
echo -e "${BLUE}►► Downloading encoder binary...${NC}"
ENCODER_BINARY_URL="https://github.com/${GITHUB_REPO}/releases/download/${LATEST_RELEASE}/encoder-linux-arm64"
if ! curl -L -o "${INSTALL_DIR}/encoder" "$ENCODER_BINARY_URL"; then
  echo -e "${RED}Failed to download encoder binary${NC}"
  exit 1
fi
chmod +x "${INSTALL_DIR}/encoder"

# Create config directory
echo -e "${BLUE}►► Setting up configuration directory...${NC}"
mkdir -p "$CONFIG_DIR"
chmod 700 "$CONFIG_DIR"

# Migrate config from old location if it exists
OLD_CONFIG="${INSTALL_DIR}/config.json"
NEW_CONFIG="${CONFIG_DIR}/config.json"
if [ -f "$OLD_CONFIG" ] && [ ! -f "$NEW_CONFIG" ]; then
  echo -e "${BLUE}►► Migrating config from old location...${NC}"
  mv "$OLD_CONFIG" "$NEW_CONFIG"
  echo -e "${GREEN}✓ Config migrated to ${NEW_CONFIG}${NC}"
fi

# Download and install systemd service
echo -e "${BLUE}►► Installing systemd service...${NC}"
if ! curl -s -o "$SERVICE_PATH" "$ENCODER_SERVICE_URL"; then
  echo -e "${RED}Failed to download service file${NC}"
  exit 1
fi

# Reload systemd and enable service
systemctl daemon-reload
systemctl enable encoder

# Start the service
echo -e "${BLUE}►► Starting encoder service...${NC}"
systemctl start encoder

# Wait for service to start
sleep 2

# Verify installation
if ! systemctl is-active --quiet encoder; then
  echo -e "${RED}Warning: Encoder service failed to start. Check logs with: journalctl -u encoder${NC}"
else
  echo -e "${GREEN}✓ Encoder service is running${NC}"
fi

# Completion message
echo -e "\n${GREEN}✓ Installation complete!${NC}"
echo -e "Open the web interface: ${BOLD}http://${FIRST_IP}:8080${NC}"
echo -e "Default credentials: ${BOLD}admin${NC} / ${BOLD}encoder${NC}"
echo -e "\nConfigure your SRT outputs via the web interface."
echo -e "The encoder will auto-start once you add at least one output.\n"
