#!/usr/bin/env bash

# Start with a clean terminal
clear

# Download the functions library
curl -s -o /tmp/functions.sh https://raw.githubusercontent.com/oszuidwest/bash-functions/main/common-functions.sh

# Source the functions file
source /tmp/functions.sh

# Set color variables
set_colors

# Check if we are root
are_we_root

# Check if this is Linux
is_this_linux
is_this_os_64bit

# Ask for input for variables
read -rp "Do you want to perform all OS updates? (default: y): " -i "y" DO_UPDATES
read -rp "Do you want to save the output of ffmpeg in a log file? (default: y): " -i "y" SAVE_OUTPUT

# Only ask for the log file and log rotation if SAVE_OUTPUT is 'y'
if [ "${SAVE_OUTPUT:-y}" = "y" ]; then
  read -rp "Which log file? (default: /var/log/ffmpeg/stream.log): " -i "/var/log/ffmpeg/stream.log" LOG_FILE
  read -rp "Do you want log rotation (daily)? (default: y): " -i "y" LOG_ROTATION
fi

# Always ask these
read -rp "Choose a port for the web interface (default: 90) " -i "90" WEB_PORT
read -rp "Choose a username for the web interface (default: admin) " -i "admin" WEB_USER
read -rp "Choose a password for the web interface (default: encoder) " -i "encoder" WEB_PASSWORD
read -rp "Choose output format: mp2, mp3, ogg/vorbis, or ogg/flac (default: ogg/flac) " -i "ogg/flac" OUTPUT_FORMAT
read -rp "Choose output server: type 1 for Icecast, type 2 for SRT (default: 1) " -i "1" OUTPUT_SERVER
read -rp "Hostname or IP address of Icecast or SRT server (default: localhost) " -i "localhost" STREAM_HOST
read -rp "Port of Icecast or SRT server (default: 8080) " -i "8080" STREAM_PORT
read -rp "Password for Icecast or SRT server (default: hackme) " -i "hackme" STREAM_PASSWORD
read -rp "Mountpoint for Icecast server or Stream ID for SRT server (default: studio) " -i "studio" STREAM_MOUNTPOINT

# Perform validation on input
validate_y_or_n DO_UPDATES SAVE_OUTPUT LOG_ROTATION
validate_port WEB_PORT
validate_port STREAM_PORT
validate_file_path LOG_FILE

if ! [[ "$OUTPUT_SERVER" =~ ^[12]$ ]]; then
  echo "Invalid value for OUTPUT_SERVER. Only '1' for Icecast or '2' for SRT are allowed."
  exit 1
fi

if ! [[ "$OUTPUT_FORMAT" =~ ^(mp2|mp3|ogg/vorbis|ogg/flac)$ ]]; then
  echo "Invalid input for OUTPUT_FORMAT. Only 'mp2', 'mp3', 'ogg/vorbis', or 'ogg/flac' are allowed."
  exit 1
fi

# Check if the DO_UPDATES variable is set to 'y'
if [ "$DO_UPDATES" == "y" ]; then
  # If it is, run the apt update, upgrade, and autoremove commands with the --yes flag to automatically answer yes to prompts
  apt -qq --yes update >/dev/null 2>&1
  apt -qq --yes upgrade >/dev/null 2>&1
  apt -qq --yes autoremove >/dev/null 2>&1
fi

# Check if logrotate should be installed
if [ "$SAVE_OUTPUT" == "y" ] && [ "$LOG_ROTATION" == "y" ]; then
  # Install ffmpeg, supervisor and logrotate
  apt -qq --yes install ffmpeg supervisor logrotate >/dev/null 2>&1
else
  # Install ffmpeg and supervisor
  apt -qq --yes install ffmpeg supervisor >/dev/null 2>&1
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
  FF_AUDIO_CODEC='libmp3lame -b:a 320k -q 0'
  FF_CONTENT_TYPE='audio/mpeg'
  FF_OUTPUT_FORMAT='mp3'
elif [ "$OUTPUT_FORMAT" == "ogg/vorbis" ]; then
  FF_AUDIO_CODEC='libvorbis -qscale:a 10'
  FF_CONTENT_TYPE='audio/ogg'
  FF_OUTPUT_FORMAT='ogg'
elif [ "$OUTPUT_FORMAT" == "ogg/flac" ]; then
  FF_AUDIO_CODEC='flac'
  FF_CONTENT_TYPE='audio/ogg'
  FF_OUTPUT_FORMAT='ogg'
fi

# Define output server for ffmpeg based on OUTPUT_SERVER
if [ "$OUTPUT_SERVER" == "1" ]; then
  FF_OUTPUT_SERVER="icecast://source:$STREAM_PASSWORD@$STREAM_HOST:$STREAM_PORT/$STREAM_MOUNTPOINT"
else
  FF_OUTPUT_SERVER="srt://$STREAM_HOST:$STREAM_PORT?pkt_size=1316&mode=caller&transtype=live&streamid=$STREAM_MOUNTPOINT&passphrase=$STREAM_PASSWORD"
fi

# Create the configuration file for supervisor
cat << EOF > /etc/supervisor/conf.d/stream.conf
  [program:encoder]
  command=bash -c "sleep 30 && ffmpeg -f alsa -channels 2 -sample_rate 48000 -hide_banner -re -y -i default:CARD=sndrpihifiberry -codec:a $FF_AUDIO_CODEC -content_type $FF_CONTENT_TYPE -vn -f $FF_OUTPUT_FORMAT "$FF_OUTPUT_SERVER""
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

# Verify installation. Set a flag to track whether any checks failed
INSTALL_FAILED=false

# Check the installation of ffmpeg
if ! command -v ffmpeg &> /dev/null; then
  echo -e "\033[31mInstallation failed. ffmpeg is not installed.\033[0m"
  INSTALL_FAILED=true
fi

# Check the installation of supervisor
if ! command -v supervisord &> /dev/null; then
  echo -e "\033[31mWInstallation failed. supervisor is not installed.\033[0m"
  INSTALL_FAILED=true
fi

# Check if the configuration file exists
if [ ! -f /etc/supervisor/conf.d/stream.conf ]; then
  echo -e "\033[31mInstallation failed. /etc/supervisor/conf.d/stream.conf does not exist.\033[0m"
  INSTALL_FAILED=true
fi

# If any checks failed, exit with an error code
if $INSTALL_FAILED; then
  exit 1
else
  # All checks passed, display success message
  echo -e "\033[32mInstallation checks passed. You can now reboot this device and streaming should start automatically.\033[0m"
fi
