#!/bin/bash

# Start with a clean terminal
clear

# Are we running on a supported platform?
if ! grep "Raspberry Pi 4" /proc/device-tree/model &> /dev/null; then
  tput bold # set text to bold
  tput setaf 1 # set text color to red
  tput blink # set text to blink
  echo "** NOT RUNNING ON A RASPBERRY PI 4 **"
  tput sgr0 # reset terminal attributes
  tput setaf 3 # set text color to yellow
  read -p "This script is only tested on a Raspberry Pi 4. Press enter to continue anyway..."
  tput sgr0 # reset terminal attributes 
exit 1
fi

# Ask for input for variables
read -p "Do you want to perform all OS updates? (default: y) " DO_UPDATES
read -p "Do you want to save the output of ffmpeg in a log file? (default: y) " SAVE_OUTPUT

# Only ask for the log file and log rotation if save output is enabled
if [ "$SAVE_OUTPUT" = "y" ]; then
  read -p "Which log file? (default: /var/log/ffmpeg/stream.log) " LOG_FILE
  read -p "Do you want log rotation (daily)? (default: y) " LOG_ROTATION
fi

# Always ask these
read -p "Choose output format: mp2, mp3, ogg/vorbis, or ogg/flac (default: ogg/flac) " OUTPUT_FORMAT
read -p "Hostname or IP address of Icecast server (default: localhost) " ICECAST_HOST
read -p "Port of Icecast server (default: 8080) " ICECAST_PORT
read -p "Password for Icecast server (default: hackme) " ICECAST_PASSWORD
read -p "Mountpoint of Icecast server (default: studio) " ICECAST_MOUNTPOINT

# If there is an empty string, use the default value
DO_UPDATES=${DO_UPDATES:-y}
SAVE_OUTPUT=${SAVE_OUTPUT:-y}
LOG_FILE=${LOG_FILE:-/var/log/ffmpeg/stream.log}
LOG_ROTATION=${LOG_ROTATION:-y}
OUTPUT_FORMAT=${OUTPUT_FORMAT:-ogg/flac}
ICECAST_HOST=${ICECAST_HOST:-localhost}
ICECAST_PORT=${ICECAST_PORT:-8000}
ICECAST_PASSWORD=${ICECAST_PASSWORD:-hackme}
ICECAST_MOUNTPOINT=${ICECAST_MOUNTPOINT:-studio}

# Perform validation on input
if [ "$DO_UPDATES" != "y" ] && [ "$DO_UPDATES" != "n" ]; then
  echo "Invalid input for DO_UPDATES. Only 'y' or 'n' are allowed."
  exit 1
fi

if [ "$SAVE_OUTPUT" != "y" ] && [ "$SAVE_OUTPUT" != "n" ]; then
  echo "Invalid input for SAVE_OUTPUT. Only 'y' or 'n' are allowed."
  exit 1
fi

if ! [[ "$LOG_FILE" =~ ^/.+/.+$ ]]; then
  echo "Invalid path for LOG_FILE. Please enter a valid path to a file (e.g. /var/log/ffmpeg/stream.log)."
  exit 1
fi

if [ "$LOG_ROTATION" != "y" ] && [ "$LOG_ROTATION" != "n" ]; then
  echo "Invalid input for LOG_ROTATION. Only 'y' or 'n' are allowed."
  exit 1
fi

if [ "$OUTPUT_FORMAT" != "mp2" ] && [ "$OUTPUT_FORMAT" != "mp3" ] && [ "$OUTPUT_FORMAT" != "ogg/vorbis" ] && [ "$OUTPUT_FORMAT" != "ogg/flac" ]; then
  echo "Invalid input for OUTPUT_FORMAT. Only 'mp2', 'mp3', 'ogg/vorbis', or 'ogg/flac' are allowed."
  exit 1
fi

if ! [[ "$ICECAST_PORT" =~ ^[0-9]+$ ]] || [ "$ICECAST_PORT" -lt 1 ] || [ "$ICECAST_PORT" -gt 65535 ]; then
  echo "Invalid port number for ICECAST_PORT. Please enter a valid port number (1 to 65535)."
  exit 1
fi

# Check if the DO_UPDATES variable is set to "y"
if [ "$DO_UPDATES" = "y" ]; then
  # If it is, run the apt update, upgrade, and autoremove commands with the --yes flag to automatically answer yes to prompts
  apt --quiet --quiet --yes update
  apt --quiet --quiet --yes upgrade
  apt --quiet --quiet --yes autoremove
fi

# Check if logrotate should be installed
if [ "$SAVE_OUTPUT" = "y" ] && [ "$LOG_ROTATION" = "y" ]; then
  # Install ffmpeg, supervisor and logrotate
  apt --quiet --quiet --yes install ffmpeg supervisor logrotate
else
  # Install ffmpeg and supervisor
  apt --quiet --quiet --yes install ffmpeg supervisor
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
  cat > /etc/logrotate.d/stream <<EOF
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

# Let ffmpeg write to /dev/null if the user doesn't want logging
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

# Create the configuration file for supervisor
cat << EOF > /etc/supervisor/conf.d/stream.conf
[program:encoder]
command=ffmpeg -f alsa -channels 2 -sample_rate 48000 -hide_banner -re -y -i default:CARD=sndrpihifiberry -codec:a $FF_AUDIO_CODEC -content_type $FF_CONTENT_TYPE -vn -f $FF_OUTPUT_FORMAT icecast://source:$ICECAST_PASSWORD@$ICECAST_HOST:$ICECAST_PORT/$ICECAST_MOUNTPOINT
autostart=true
autorestart=true
startretries=9999999999999999999999999999999999999999999999999
redirect_stderr=true
stdout_logfile_maxbytes=0MB
stdout_logfile_backups=0
stdout_logfile=$LOG_PATH
EOF
