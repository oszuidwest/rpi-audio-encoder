#!/bin/bash

# Ask user for input for variables
read -p "Do you want to perform all OS updates? (default: y) " do_updates
read -p "Do you want to save the output of ffmpeg in a log file? (default: y) " save_output
read -p "Which log file? (default: /var/log/ffmpeg/stream.log) " log_file
read -p "Do you want log rotation (daily)? (default: y) " log_rotation
read -p "Choose output format: mp3, ogg/vorbis, or ogg/flac (default: ogg/flac) " output_format
read -p "Hostname or IP address of Icecast server (default: localhost) " icecast_host
read -p "Port of Icecast server (default: 8080) " icecast_port
read -p "Password for Icecast server (default: hackme) " icecast_password
read -p "Mountpoint of Icecast server (default: studio) " icecast_mountpoint

# If the user enters an empty string, use the default value
do_updates=${do_updates:-y}
save_output=${save_output:-y}
log_file=${log_file:-/var/log/ffmpeg/stream.log}
log_rotation=${log_rotation:-y}
output_format=${output_format:-ogg/flac}
icecast_host=${icecast_host:-localhost}
icecast_port=${icecast_port:-8000}
icecast_password=${icecast_password:-hackme}
icecast_mountpoint=${icecast_mountpoint:-studio}

# Perform validation on input
if [ "$do_updates" != "y" ] && [ "$do_updates" != "n" ]; then
  echo "Invalid input for do_updates. Only 'y' or 'n' are allowed."
  exit 1
fi

if [ "$save_output" != "y" ] && [ "$save_output" != "n" ]; then
  echo "Invalid input for save_output. Only 'y' or 'n' are allowed."
  exit 1
fi

if ! [[ "$log_file" =~ ^/[^/ ]+/[^/ ]+$ ]]; then
  echo "Invalid path for log_file. Please enter a valid path to a file (e.g. /var/log/ffmpeg/stream.log)."
  exit 1
fi

if [ "$log_rotation" != "y" ] && [ "$log_rotation" != "n" ]; then
  echo "Invalid input for log_rotation. Only 'y' or 'n' are allowed."
  exit 1
fi

if [ "$output_format" != "mp3" ] && [ "$output_format" != "ogg/vorbis" ] && [ "$output_format" != "ogg/flac" ]; then
  echo "Invalid input for output_format. Only 'mp3', 'ogg/vorbis', or 'ogg/flac' are allowed."
  exit 1
fi

# Check if the given port number is a valid port number (1 to 65535)
if ! [[ "$icecast_port" =~ ^[0-9]+$ ]] || [ "$icecast_port" -lt 1 ] || [ "$icecast_port" -gt 65535 ]; then
  echo "Invalid port number for icecast_port. Please enter a valid port number (1 to 65535)."
  exit 1
fi

# Check if the do_updates variable is set to "y"
if [ "$do_updates" = "y" ]; then
  # If it is, run the apt update, upgrade, and autoremove commands with the -y flag to automatically answer yes to prompts
  apt update -y
  apt upgrade -y
  apt autoremove -y
fi

# Check if logrotate should be installed
if [ "$save_output" = "y" ] && [ "$log_rotation" = "y" ]; then
  # Install ffmpeg, supervisor and logrotate
  apt-get install ffmpeg supervisor logrotate
else
  # Install ffmpeg and supervisor
  apt-get install ffmpeg supervisor
fi
