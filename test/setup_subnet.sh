#!/bin/bash
# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

#
# This script makes sure that 127.0.0.x is routable. On Darwin, there
# is a bug that it isn't routable and this causes errors.
#

action=${1:-up}

if [  "$action" = "up" ]
then
  # Check if loopback is setup
  ping -c 1 -W 10 127.0.0.2 > /dev/null 2>&1
  if [ $? -eq 0 ]
  then
      exit
  fi
fi

# If we're not on OS X, then error
case $OSTYPE in
    darwin*)
        ;;
    *)
        echo "Can't setup interfaces on non-Mac. Error!"
        exit 1
        ;;
esac

# first loopback, use intensively
for ((i=10;i<256;i++))
  do
      if [ "$action" = "up" ]
      then
        sudo ifconfig lo0 alias 127.0.0.$i up
      else
        sudo ifconfig lo0 127.0.0.$i delete
     fi
  done

# Not use much, only need a few
for j in 1 2
do
  for ((i=10;i<15;i++))
  do
      if [ "$action" = "up" ]
      then
        sudo ifconfig lo0 alias 127.0.$j.$i up
      else
        sudo ifconfig lo0 127.0.$j.$i delete
     fi
  done
done

