#!/usr/bin/env bash

# Set-up the functions library
FUNCTIONS_LIB_PATH="/tmp/functions.sh"
FUNCTIONS_LIB_URL="https://raw.githubusercontent.com/oszuidwest/bash-functions/main/common-functions.sh"

# Set-up RAM disk
RAMDISK_SERVICE_PATH="/etc/systemd/system/ramdisk.service"
RAMDISK_SERVICE_URL="https://raw.githubusercontent.com/oszuidwest/rpi-audio-encoder/main/ramdisk.service"
RAMDISK_PATH="/mnt/ramdisk"

# Set-up FFmpeg and Supervisor
LOGROTATE_CONFIG_PATH="/etc/logrotate.d/stream"
STREAM_CONFIG_PATH="/etc/supervisor/conf.d/stream.conf"
STREAM_LOG_PATH="/var/log/ffmpeg/stream.log"
SUPERVISOR_CONFIG_PATH="/etc/supervisor/supervisord.conf"

# General Raspberry Pi configuration
CONFIG_FILE_PATHS=("/boot/firmware/config.txt" "/boot/config.txt")
FIRST_IP=$(hostname -I | awk '{print $1}')

# Start with a clean terminal
clear

# Remove old functions library and download the latest version
rm -f "$FUNCTIONS_LIB_PATH"
if ! curl -s -o "$FUNCTIONS_LIB_PATH" "$FUNCTIONS_LIB_URL"; then
  echo -e "*** Failed to download functions library. Please check your network connection! ***"
  exit 1
fi

# Source the functions file
# shellcheck source=/tmp/functions.sh
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

# Ask for input for variables
ask_user "DO_UPDATES" "y" "Do you want to perform all OS updates? (y/n)" "y/n"
ask_user "SAVE_OUTPUT" "y" "Do you want to save the output of ffmpeg to a log file? (y/n)" "y/n"
ask_user "ENABLE_HEARTBEAT" "n" "Do you want to integrate heartbeat monitoring via UptimeRobot (y/n)" "y/n"
if [ "$ENABLE_HEARTBEAT" == "y" ]; then
  ask_user "HEARTBEAT_URL" "https://heartbeat.uptimerobot.com/xxx" "Enter the URL to get every minute for heartbeat monitoring" "str"
fi

# Always ask these
ask_user "WEB_PORT" "90" "Choose a port for the web interface" "num"
ask_user "WEB_USER" "admin" "Choose a username for the web interface" "str"
ask_user "WEB_PASSWORD" "encoder" "Choose a password for the web interface" "str"
ask_user "OUTPUT_FORMAT" "wav" "Choose output format: mp2, mp3, ogg, or wav" "str"
ask_user "STREAM_HOST" "localhost" "Hostname or IP address of SRT server" "str"
ask_user "STREAM_PORT" "8080" "Port of SRT server" "num"
ask_user "STREAM_PASSWORD" "hackme" "Password for SRT server" "str"
ask_user "STREAM_MOUNTPOINT" "studio" "Stream ID for SRT server" "str"

if ! [[ "$OUTPUT_FORMAT" =~ ^(mp2|mp3|ogg|wav)$ ]]; then
  echo "Invalid input for OUTPUT_FORMAT. Only 'mp2', 'mp3', 'ogg', or 'wav' are allowed."
  exit 1
fi

# Timezone configuration
set_timezone Europe/Amsterdam

# Check if the DO_UPDATES variable is set to 'y'
if [ "$DO_UPDATES" == "y" ]; then
  update_os silent
fi

# Install dependencies
if [ "$SAVE_OUTPUT" == "y" ]; then
  install_packages silent ffmpeg supervisor logrotate
else
  install_packages silent ffmpeg supervisor
fi

# Check if 'SAVE_OUTPUT' is set to 'y'
if [ "$SAVE_OUTPUT" == "y" ]; then
  # Parse the value of 'STREAM_LOG_PATH' to just the directory
  STREAM_LOG_DIR=$(dirname "$STREAM_LOG_PATH")
  # If the directory doesn't exist, create it
  if [ ! -d "$STREAM_LOG_DIR" ]; then
    mkdir -p "$STREAM_LOG_DIR"
  fi
fi

# Set-up logrotate if logging is enabled
if [ "$SAVE_OUTPUT" == "y" ]; then
  cat << EOF > $LOGROTATE_CONFIG_PATH
$STREAM_LOG_PATH {
  daily
  rotate 14
  copytruncate
  compress
  missingok
  notifempty
}
EOF
fi

# Let ffmpeg write to /dev/null if logging is disabled
if [ "$SAVE_OUTPUT" == "y" ]; then
  LOG_PATH="$STREAM_LOG_PATH"
else
  LOG_PATH="/dev/null"
fi

# Set the ffmpeg variables based on the value of OUTPUT_FORMAT
if [ "$OUTPUT_FORMAT" == "mp2" ]; then
  FF_AUDIO_CODEC='libtwolame -b:a 384k -psymodel 4'
  FF_CONTENT_TYPE='audio/mpeg'
  FF_OUTPUT_FORMAT='mp2'
elif [ "$OUTPUT_FORMAT" == "mp3" ]; then
  FF_AUDIO_CODEC='libmp3lame -b:a 320k'
  FF_CONTENT_TYPE='audio/mpeg'
  FF_OUTPUT_FORMAT='mp3'
elif [ "$OUTPUT_FORMAT" == "ogg" ]; then
  FF_AUDIO_CODEC='libvorbis -qscale:a 10'
  FF_CONTENT_TYPE='audio/ogg'
  FF_OUTPUT_FORMAT='ogg'
elif [ "$OUTPUT_FORMAT" == "wav" ]; then
  FF_AUDIO_CODEC='pcm_s16le'
  FF_CONTENT_TYPE='audio/x-wav'
  FF_OUTPUT_FORMAT='wav'
fi

# Define output server for ffmpeg
FF_OUTPUT_SERVER="srt://$STREAM_HOST:$STREAM_PORT?pkt_size=1316&oheadbw=100&maxbw=-1&latency=10000000&mode=caller&transtype=live&streamid=$STREAM_MOUNTPOINT&passphrase=$STREAM_PASSWORD"

# Add RAM disk
if [ "$SAVE_OUTPUT" == "y" ]; then
  echo -e "${BLUE}►► Setting up RAM disk for logs...${NC}"
  rm -f "$RAMDISK_SERVICE_PATH" > /dev/null
  curl -s -o "$RAMDISK_SERVICE_PATH" "$RAMDISK_SERVICE_URL"
  systemctl daemon-reload > /dev/null
  systemctl enable ramdisk > /dev/null
  systemctl start ramdisk
fi

# Put FFmpeg logs on RAM disk
if [ "$SAVE_OUTPUT" == "y" ]; then
  echo -e "${BLUE}►► Putting FFmpeg logs on the RAM disk...${NC}"
  if [ -d "$STREAM_LOG_DIR" ]; then
    echo -e "${YELLOW}Log directory exists. Removing it before creating the symlink.${NC}"
    rm -rf "$STREAM_LOG_DIR"
    ln -s "$RAMDISK_PATH" "$STREAM_LOG_DIR"
  fi
fi

# Create the configuration file for supervisor
cat << EOF > $STREAM_CONFIG_PATH
  [program:encoder]
  command=bash -c "sleep 30 && ffmpeg -f alsa -channels 2 -sample_rate 48000 -hide_banner -re -y -i default:CARD=sndrpihifiberry -codec:a $FF_AUDIO_CODEC -content_type $FF_CONTENT_TYPE -vn -f $FF_OUTPUT_FORMAT '$FF_OUTPUT_SERVER'"
  # Sleep 30 seconds before starting ffmpeg because the network or audio might not be available after a reboot. Works for now, should dig in the exact cause in the future.
  autostart=true
  autorestart=true
  startretries=999999999
  redirect_stderr=true
  stdout_logfile_maxbytes=0MB
  stdout_logfile_backups=0
  stdout_logfile=$LOG_PATH
EOF

# Configure the web interface
if ! grep -q "\[inet_http_server\]" $SUPERVISOR_CONFIG_PATH; then
  sed -i "/\[supervisord\]/i\
  [inet_http_server]\n\
  port = 0.0.0.0:$WEB_PORT\n\
  username = $WEB_USER\n\
  password = $WEB_PASSWORD\n\
  " $SUPERVISOR_CONFIG_PATH
  # Tidy up file after wrting to it
  sed -i 's/^[ \t]*//' $SUPERVISOR_CONFIG_PATH
fi

# Heartbeat monitoring
if [ "$ENABLE_HEARTBEAT" == "y" ]; then
  echo -e "${BLUE}►► Setting up heartbeat monitoring...${NC}"
  HEARTBEAT_CRONJOB="* * * * * wget --spider $HEARTBEAT_URL > /dev/null 2>&1"
  if ! crontab -l | grep -F -- "$HEARTBEAT_CRONJOB" > /dev/null; then
    (crontab -l 2>/dev/null; echo "$HEARTBEAT_CRONJOB") | crontab -
  else
    echo -e "${YELLOW}Heartbeat monitoring cronjob already exists. No changes made.${NC}"
  fi
fi

# Check the installation of ffmpeg and supervisord
require_tool ffmpeg supervisord

# Check if the configuration file exists
# @ TODO: USE A MORE COMPREHENSIVE CHECK FUNCTION THAT CHECKS COMMANDS OR FILES
if [ ! -f $STREAM_CONFIG_PATH ]; then
  echo -e "${RED}Installation failed. $STREAM_CONFIG_PATH does not exist.${NC}" >&2
  exit 1
fi

# Completion message
echo -e "\n${GREEN}✓ Success!${NC}"
echo -e "Reboot to start streaming to $STREAM_HOST. Web interface: http://${FIRST_IP}:$WEB_PORT."
echo -e "User: ${BOLD}$WEB_USER${NC}, password: ${BOLD}$WEB_PASSWORD${NC}.\n"
