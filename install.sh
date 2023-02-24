#!/bin/bash

# Start with a clean terminal
clear

# Is this a supported platform?
if ! grep "Raspberry Pi 4" /proc/device-tree/model &> /dev/null; then
  echo -e "\e[1;31;5m** NOT RUNNING ON A RASPBERRY PI 4 **\e[0m"
  echo -e "\e[3m\e[33mThis script is only tested on a Raspberry Pi 4. Press enter to continue anyway...\e[0m"
fi

# Function that checks if a variable is y or n
function var_is_y_or_n {
  local var_name var invalid_vars first_time
  first_time=1
  for var_name in "$@"
  do
    var="${!var_name}"
    if [ "$var" != "y" ] && [ "$var" != "n" ]; then
      if [ "$first_time" -eq 1 ]; then
        invalid_vars="$var_name"
        first_time=0
      else
        invalid_vars="$invalid_vars, $var_name"
      fi
    fi
  done
  if [ -n "$invalid_vars" ]; then
    echo "Invalid input for the following option(s): $invalid_vars. Only 'y' or 'n' are allowed."
    exit 1
  fi
}

# Ask for input for variables
read -rp "Do you want to perform all OS updates? (default: y) " DO_UPDATES
read -rp "Do you want to save the output of ffmpeg in a log file? (default: y) " SAVE_OUTPUT

# Only ask for the log file and log rotation if SAVE_OUTPUT is 'y'
if [ "$SAVE_OUTPUT" = "y" ]; then
  read -rp "Which log file? (default: /var/log/ffmpeg/stream.log) " LOG_FILE
  read -rp "Do you want log rotation (daily)? (default: y) " LOG_ROTATION
fi

# Always ask these
read -rp "Choose a port for the web interface (default: 90) " WEB_PORT
read -rp "Choose a username for the web interface (default: admin) " WEB_USER
read -rp "Choose a password for the web interface (default: encoder) " WEB_PASSWORD
read -rp "Choose output format: mp2, mp3, ogg/vorbis, or ogg/flac (default: ogg/flac) " OUTPUT_FORMAT
read -rp "Choose output server: type 1 for Icecast, type 2 for SRT (default: 1)" OUTPUT_SERVER
read -rp "Hostname or IP address of Icecast or SRT server (default: localhost) " STREAM_HOST
read -rp "Port of Icecast or SRT server (default: 8080) " STREAM_PORT
read -rp "Password for Icecast or SRT server (default: hackme) " STREAM_PASSWORD
read -rp "Mountpoint for Icecast server or Stream ID for SRT server (default: studio) " STREAM_MOUNTPOINT

# Set defaults
DO_UPDATES=${DO_UPDATES:-y}
SAVE_OUTPUT=${SAVE_OUTPUT:-y}
LOG_FILE=${LOG_FILE:-/var/log/ffmpeg/stream.log}
LOG_ROTATION=${LOG_ROTATION:-y}
OUTPUT_FORMAT=${OUTPUT_FORMAT:-ogg/flac}
OUTPUT_SERVER=${OUTPUT_SERVER:-1}
WEB_PORT=${WEB_PORT:-90}
WEB_USER=${WEB_USER:-admin}
WEB_PASSWORD=${WEB_PASSWORD:-encoder}
STREAM_HOST=${STREAM_HOST:-localhost}
STREAM_PORT=${STREAM_PORT:-8000}
STREAM_PASSWORD=${STREAM_PASSWORD:-hackme}
STREAM_MOUNTPOINT=${STREAM_MOUNTPOINT:-studio}

# Perform validation on input
var_is_y_or_n "$DO_UPDATES" "$SAVE_OUTPUT" "$LOG_ROTATION"

if ! [[ "$WEB_PORT" =~ ^[0-9]+$ ]] || [ "$WEB_PORT" -lt 1 ] || [ "$WEB_PORT" -gt 65535 ]; then
  echo "Invalid port number for WEB_PORT. Please enter a valid port number (1 to 65535)."
  exit 1
fi

if ! [[ "$STREAM_PORT" =~ ^[0-9]+$ ]] || [ "$STREAM_PORT" -lt 1 ] || [ "$STREAM_PORT" -gt 65535 ]; then
  echo "Invalid port number for STREAM_PORT. Please enter a valid port number (1 to 65535)."
  exit 1
fi

if ! [[ "$LOG_FILE" =~ ^/.+/.+$ ]]; then
  echo "Invalid path for LOG_FILE. Please enter a valid path to a file (e.g. /var/log/ffmpeg/stream.log)."
  exit 1
fi

if [ "$OUTPUT_SERVER" != "1" ] && [ "$OUTPUT_SERVER" != "2" ]; then
  echo "Invalid value for OUTPUT_SERVER. Only '1' for Icecast or '2' for SRT are allowed."
  exit 1
fi

if [ "$OUTPUT_FORMAT" != "mp2" ] && [ "$OUTPUT_FORMAT" != "mp3" ] && [ "$OUTPUT_FORMAT" != "ogg/vorbis" ] && [ "$OUTPUT_FORMAT" != "ogg/flac" ]; then
  echo "Invalid input for OUTPUT_FORMAT. Only 'mp2', 'mp3', 'ogg/vorbis', or 'ogg/flac' are allowed."
  exit 1
fi

# Check if the DO_UPDATES variable is set to 'y'
if [ "$DO_UPDATES" = "y" ]; then
  # If it is, run the apt update, upgrade, and autoremove commands with the --yes flag to automatically answer yes to prompts
  apt --quiet --quiet --yes update >/dev/null 2>&1
  apt --quiet --quiet --yes upgrade >/dev/null 2>&1
  apt --quiet --quiet --yes autoremove >/dev/null 2>&1
fi

# Check if logrotate should be installed
if [ "$SAVE_OUTPUT" = "y" ] && [ "$LOG_ROTATION" = "y" ]; then
  # Install ffmpeg, supervisor and logrotate
  apt --quiet --quiet --yes install ffmpeg supervisor logrotate >/dev/null 2>&1
else
  # Install ffmpeg and supervisor
  apt --quiet --quiet --yes install ffmpeg supervisor >/dev/null 2>&1
fi

# Check if 'SAVE_OUTPUT' is set to 'y'
if [ "$SAVE_OUTPUT" = "y" ]; then
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
if [ "$SAVE_OUTPUT" = "y" ]; then
  LOG_PATH=$LOG_FILE
else
  LOG_PATH="/dev/null"
fi

# Set the ffmpeg variables based on the value of OUTPUT_FORMAT
if [ "$OUTPUT_FORMAT" = "mp2" ]; then
  FF_AUDIO_CODEC='libtwolame -b:a 384k -psymodel 4'
  FF_CONTENT_TYPE='audio/mpeg'
  FF_OUTPUT_FORMAT='mp2'
elif [ "$OUTPUT_FORMAT" = "mp3" ]; then
  FF_AUDIO_CODEC='libmp3lame -b:a 320k -q 0'
  FF_CONTENT_TYPE='audio/mpeg'
  FF_OUTPUT_FORMAT='mp3'
elif [ "$OUTPUT_FORMAT" = "ogg/vorbis" ]; then
  FF_AUDIO_CODEC='libvorbis -qscale:a 10'
  FF_CONTENT_TYPE='audio/ogg'
  FF_OUTPUT_FORMAT='ogg'
elif [ "$OUTPUT_FORMAT" = "ogg/flac" ]; then
  FF_AUDIO_CODEC='flac'
  FF_CONTENT_TYPE='audio/ogg'
  FF_OUTPUT_FORMAT='ogg'
fi

# Define output server for ffmpeg based on OUTPUT_SERVER
if [ "$OUTPUT_SERVER" = "1" ]; then
  FF_OUTPUT_SERVER="icecast://source:$STREAM_PASSWORD@$STREAM_HOST:$STREAM_PORT/$STREAM_MOUNTPOINT"
else
  FF_OUTPUT_SERVER="srt://$STREAM_HOST:$STREAM_PORT?pkt_size=1316&mode=caller&transtype=live&streamid=$STREAM_MOUNTPOINT&passphrase=$STREAM_PASSWORD"
fi

# Create the configuration file for supervisor
cat << EOF > /etc/supervisor/conf.d/stream.conf
  [program:encoder]
  command=bash -c "sleep 30 && ffmpeg -f alsa -channels 2 -sample_rate 48000 -hide_banner -re -y -i default:CARD=sndrpihifiberry -codec:a $FF_AUDIO_CODEC -content_type $FF_CONTENT_TYPE -vn -f $FF_OUTPUT_FORMAT $FF_OUTPUT_SERVER"
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
