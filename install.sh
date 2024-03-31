#!/usr/bin/env bash

# Initialize the environment
clear
rm -f /tmp/functions.sh
if ! curl -s -o /tmp/functions.sh https://raw.githubusercontent.com/oszuidwest/bash-functions/main/common-functions.sh; then
  echo "*** Failed to download functions library. Please check your network connection! ***"
  exit 1
fi

# Source the functions file
source /tmp/functions.sh

# Set color variables
set_colors

# Check if we are root
are_we_root

# Check if this is Linux
is_this_linux
is_this_os_64bit

# Check if we are running on a Raspberry Pi 3 or newer
check_rpi_model 3

# Something fancy for the sysadmin
cat << "EOF"
 ______     _     ___          __       _     ______ __  __ 
|___  /    (_)   | \ \        / /      | |   |  ____|  \/  |
   / /_   _ _  __| |\ \  /\  / /__  ___| |_  | |__  | \  / |
  / /| | | | |/ _` | \ \/  \/ / _ \/ __| __| |  __| | |\/| |
 / /_| |_| | | (_| |  \  /\  /  __/\__ \ |_  | |    | |  | |
/_____\__,_|_|\__,_|   \/  \/ \___||___/\__| |_|    |_|  |_|
EOF

# Hi! Let's check the OS
os_name=$(grep '^NAME=' /etc/os-release | cut -d'=' -f2 | tr -d '"')
os_codename=$(grep '^UBUNTU_CODENAME=' /etc/os-release | cut -d'=' -f2 | tr -d '"')

if [[ "$os_name" == "Ubuntu" && "$os_codename" == "jammy" ]]; then
  echo -e "${GREEN}⎎ Audio encoder set-up for Raspberry Pi${NC}\n"
else
  echo -e "${RED}This script only supports Ubuntu 22.04 LTS! Exiting...${NC}\n" >&2
  exit 1
fi

# Check if the HiFiBerry is configured
if [ ! -f "/boot/config.txt" ] || ! grep -q "^dtoverlay=hifiberry" "/boot/config.txt"; then
  if [ ! -f "/boot/firmware/config.txt" ] || ! grep -q "^dtoverlay=hifiberry" "/boot/firmware/config.txt"; then
    echo -e "${RED}No HiFiBerry card configured in the config.txt file. Exiting...${NC}\n" >&2
    exit 1
  fi
fi

# Ask for input for variables
ask_user "DO_UPDATES" "y" "Do you want to perform all OS updates? (y/n)" "y/n"
ask_user "SAVE_OUTPUT" "y" "Do you want to save the output of ffmpeg in a log file? (y/n)" "y/n"

# Only ask for the log file and log rotation if SAVE_OUTPUT is 'y'
if [ "${SAVE_OUTPUT}" == "y" ]; then
  ask_user "LOG_FILE" "/var/log/ffmpeg/stream.log" "Which log file?" "str"
  ask_user "LOG_ROTATION" "y" "Do you want log rotation (daily)?" "y/n"
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

# Check if logrotate should be installed
if [ "$SAVE_OUTPUT" == "y" ] && [ "$LOG_ROTATION" == "y" ]; then
  # Install ffmpeg, supervisor and logrotate
  install_packages silent ffmpeg supervisor logrotate
else
  # Install ffmpeg and supervisor
  install_packages silent ffmpeg supervisor
fi

# Check if 'SAVE_OUTPUT' is set to 'y'
if [ "$SAVE_OUTPUT" == "y" ]; then
  # Parse the value of 'LOG_FILE' to just the directory
  LOG_DIR=$(dirname "$LOG_FILE")
  # If the directory doesn't exist, create it
  if [ ! -d "$LOG_DIR" ]; then
    mkdir -p "$LOG_DIR"
  fi
fi

# Check if SAVE_OUTPUT is 'y' and LOG_ROTATION is 'y'
if [ "$SAVE_OUTPUT" == "y" ] && [ "$LOG_ROTATION" == "y" ]; then
  # If is is, configure logrotate
  cat << EOF > /etc/logrotate.d/stream
$LOG_FILE {
  daily
  rotate 30
  copytruncate
  nocompress
  missingok
  notifempty
}
EOF
fi

# Let ffmpeg write to /dev/null if logging is disabled
if [ "$SAVE_OUTPUT" == "y" ]; then
  LOG_PATH="$LOG_FILE"
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
FF_OUTPUT_SERVER="srt://$STREAM_HOST:$STREAM_PORT?pkt_size=1316&mode=caller&transtype=live&streamid=$STREAM_MOUNTPOINT&passphrase=$STREAM_PASSWORD"

# Create the configuration file for supervisor
cat << EOF > /etc/supervisor/conf.d/stream.conf
  [program:encoder]
  command=bash -c "sleep 30 && ffmpeg -f alsa -channels 2 -sample_rate 48000 -hide_banner -re -y -i default:CARD=sndrpihifiberry -codec:a $FF_AUDIO_CODEC -content_type $FF_CONTENT_TYPE -vn -f $FF_OUTPUT_FORMAT '$FF_OUTPUT_SERVER'"
  # Sleep 30 seconds before starting ffmpeg because the network or audio might not be available after a reboot. Works for now, should dig in the exact cause in the future.
  autostart=true
  autorestart=true
  startretries=9999999999999999999999999999999999999999999999999
  redirect_stderr=true
  stdout_logfile_maxbytes=0MB
  stdout_logfile_backups=0
  stdout_logfile=$LOG_PATH
EOF

# Configure the web interface
if ! grep -q "\[inet_http_server\]" /etc/supervisor/supervisord.conf; then
  sed -i "/\[supervisord\]/i\
  [inet_http_server]\n\
  port = 0.0.0.0:$WEB_PORT\n\
  username = $WEB_USER\n\
  password = $WEB_PASSWORD\n\
  " /etc/supervisor/supervisord.conf
  # Tidy up file after wrting to it
  sed -i 's/^[ \t]*//' /etc/supervisor/supervisord.conf
fi

# Check the installation of ffmpeg and supervisord
check_required_command ffmpeg supervisord

# Check if the configuration file exists
# @ TODO: USE A MORE COMPREHENSIVE CHECK FUNCTION THAT CHECKS COMMANDS OR FILES
if [ ! -f /etc/supervisor/conf.d/stream.conf ]; then
  echo -e "${RED}Installation failed. /etc/supervisor/conf.d/stream.conf does not exist.${NC}" >&2
  exit 1
fi

# Fin 
echo -e "\n${GREEN}✓ Success!${NC}"
echo -e "Reboot this device and streaming to ${BOLD}$STREAM_HOST${NC} should start."
echo -e "You can connect to it's IP in the brower on port ${BOLD}$WEB_PORT${NC}."
echo -e "The user is ${BOLD}$WEB_USER${NC} and the password you choose is ${BOLD}$WEB_PASSWORD${NC}.\n"
