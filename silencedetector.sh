#!/bin/bash

# Set the threshold (in dB) and length for detecting silence 
THRESHOLD=-40
DURATION=5

# Record audio from the default input and output volume
mean_volume=$(ffmpeg -hide_banner -f alsa -channels 2 -sample_rate 48000 -hide_banner -i default:CARD=sndrpihifiberry -t "$DURATION" -f wav -af "volumedetect" -f null /dev/null 2>&1 | \

# Extract the mean_volume value from the output of ffmpeg
grep -oP '(?<=mean_volume: ).*(?= dB)' | \

# Round it because bash doesn't support floating-point arithmetic
awk '{printf "%.0f", $1}')

# Verify that the mean_volume extraction works. It should be a number that starts with the "-" sign
if [[ $mean_volume =~ ^-([0-9]+) ]]; then
  # Compare the mean_volume to the threshold to determine if it is silent
  if [[ $mean_volume -lt $THRESHOLD ]]; then
    echo "silent"
  else
    echo "not silent"
  fi
else
  # If the regular expression does not match, bail
  echo "Invalid mean_volume value: $mean_volume"
fi
