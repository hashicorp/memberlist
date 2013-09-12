#!/bin/bash

# Check if loopback is setup
ping -c 1 -W 10 127.0.0.2 > /dev/null 2>&1
if [ $? -eq 0 ]
then
    exit
fi

# Setup loopback
for ((i=2;i<256;i++))
do
    sudo ifconfig lo0 alias 127.0.0.$i up
done
