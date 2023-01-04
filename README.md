# rpi-audio-encoder
This repository contains the audio streaming software for [ZuidWest FM](https://www.zuidwestfm.nl/) in the Netherlands. It uses a Rapsberry Pi 4 and a [HiFiBerry Digi+ I/O](https://www.hifiberry.com/shop/boards/hifiberry-digi-io/) as audio input. As encoder ffmpeg is used, which is combined with Supervisor to manage the process via a webinterface.

This encoder resides in the studio and is connected to an Optimod. It can stream to any Icecast server. Our server software to complete the audio stack can be found in [this respository](https://github.com/oszuidwest/liquidsoap-ubuntu).

<img src="https://web.archive.org/web/20221225231636if_/https://j6z7x9q7.rocketcdn.me/wp-content/uploads/2022/04/Stalen-behuizing-HiFiBerry-Pi4-Digi-1.jpg" width=30% height=30%>

# How to prepare the Rapsberry Pi
- Install Raspberry Pi OS Lite 11 (bullseye) 64-bit
- Run `sudo raspi-config` to set timezone, Wi-Fi country and expand the filesystem
- Follow the guide on https://www.hifiberry.com/docs/software/configuring-linux-3-18-x/ to set-up the HiFiBerry
- Ensure you are root by running `sudo su`
- Download and run the install script with the command `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/oszuidwest/rpi-encoder/main/install.sh)"`

## ⚠️ This is considered experimental ⚠️
We run this in production, but there are known bugs. The biggest one is that ffmpeg doesn't seem to be able to stream after a reboot. You have to restart it via the web interface this first time after a reboot.
