#!/bin/bash

# Set the threshold (in dB) and length for detecting silence 
THRESHOLD=-40
DURATION=5

# Record audio from the default microphone and calculate the RMS power using ffmpeg
ffmpeg -hide_banner -f alsa -channels 2 -sample_rate 48000 -hide_banner -i default:CARD=sndrpihifiberry -t "$DURATION" -f wav -af "volumedetect" -f null /dev/null 2>&1 | \

# Extract the RMS power value from the output of ffmpeg
grep "mean_volume" | \

# Extract the RMS power value and convert it to dB
awk '{print $4}' | awk '{print 20*log($1)/log(10)}' | \

# Compare the RMS power value to the threshold to determine if it is silent
awk -v threshold="$THRESHOLD" '{if ($1 < threshold) print "silent"; else print "not silent"}'
