#!/bin/bash

# Set the threshold (in dB) and length for detecting silence 
THRESHOLD=-40
DURATION=5

# Record audio from the default microphone and calculate the RMS power using ffmpeg
ffmpeg -hide_banner -f alsa -channels 2 -sample_rate 48000 -hide_banner -i default:CARD=sndrpihifiberry -t "$DURATION" -f wav -af "volumedetect" -f null /dev/null 2>&1 | \

# Extract the RMS power value from the output of ffmpeg
grep -oP '(?<=mean_volume: ).*(?= dB)' | \

# Compare the mean_volume to the threshold to determine if it is silent
awk -v threshold="$THRESHOLD" '{if ($1 < threshold) print "silent"; else print "not silent"}'
