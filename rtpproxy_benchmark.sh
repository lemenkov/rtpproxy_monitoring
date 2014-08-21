#!/bin/bash

NUM=$1
RHOST=127.0.0.1
RPORT=22222

. /etc/sysconfig/rtpproxy_benchmark

for i in `seq 1 ${NUM}`
do
	HPORT=$((8080+i))
	/usr/bin/rtpproxy_monitoring -syslog=false -hport=${HPORT} -rhost="${RHOST}" -rport="${RPORT}" -ptype=8 -psize=160 -htime=60 -hsize=10 > /dev/null 2>&1 & 
done
