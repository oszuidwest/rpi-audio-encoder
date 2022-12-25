# rpi-encoder
Can we replace our bulky rackmount audio encoder with a Raspberry Pi? This project tries to explore that.

# How to prepare the Rapsberry Pi
- Install Raspberry Pi OS Lite 11 (bullseye) 64-bit
- Run `sudo raspi-config` to set timezone, Wi-Fi country and expand the filesystem
- Follow the guide on https://www.hifiberry.com/docs/software/configuring-linux-3-18-x/ to set-up the HiFiBerry
- Ensure you are root by running `sudo su`
- Download and run the install script with the command `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/oszuidwest/rpi-encoder/main/silencedetector.sh)"`

## ⚠️ This is considered experimental ⚠️
We run this in production, but there are known bugs. The biggest one is that ffmpeg doesn't seem to be able to stream after a reboot. You have to restart it via the web interface this first time after a reboot.