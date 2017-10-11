#!/bin/sh -x

version=$(go version|grep -Eo go[0-9\.]+)

if [ "$version" != "go1.9" ]; then
    echo "Container are only generated for version 1.9 and you have ${version}."
    exit 0
fi

if [ "${DOCKER_REPO}" = "" ]; then
    DOCKER_REPO=fclairamb/ftpserver
    TRAVIS_BRANCH=whatever
    TRAVIS_COMMIT=whatever
fi

me=$0
target=$1

GOOS=linux
BINARY=ftpserver
BINARY_TARGET=/bin/ftpserver
CONF_FILE=/etc/ftpserver.conf
DATA_DIR=/data

if [ "${target}" = "" ]; then
    for t in std rpi win
    do
        ${me} ${t}
    done
    exit 0
elif [ "${target}" = "std" ] ; then
    GOARCH=amd64
    BINARY=ftpserver
    DOCKER_FROM=alpine:3.6
    DOCKER_TAG_PREFIX=
elif [ "${target}" = "rpi" ]; then
    GOARCH=arm
    DOCKER_FROM=multiarch/alpine:arm-3.6
    DOCKER_TAG_PREFIX=arm-
elif [ "${target}" = "win" ]; then
    GOOS=windows
    GOARCH=amd64
    BINARY=ftpserver.exe
    BINARY_TARGET=C:\ftpserver.exe
    DOCKER_FROM=microsoft/nanoserver
    DOCKER_TAG_PREFIX=win-
fi

CGO_ENABLED=0 go build -a -installsuffix cgo
# GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -a -installsuffix cgo
# GOOS=linux GOARCH=arm CGO_ENABLED=0 go build -a -installsuffix cgo

echo "
FROM ${DOCKER_FROM}
EXPOSE 2121-2200
COPY sample/conf/settings.toml ${CONF_FILE}
CMD mkdir -p ${DATA_DIR}
COPY ${BINARY} ${BINARY_TARGET}
ENTRYPOINT [ \"${BINARY_TARGET}\", \"-conf=${CONF_FILE}\", \"-data=${DATA_DIR}\" ]
" >Dockerfile

echo "Docker repo: ${DOCKER_REPO}:${TRAVIS_COMMIT}"

DOCKER_NAME=${DOCKER_REPO}:${TRAVIS_COMMIT}

docker build -t ${DOCKER_NAME} .

docker tag ${DOCKER_NAME} ${DOCKER_REPO}:travis-${TRAVIS_BUILD_NUMBER}

if [ "${TRAVIS_TAG}" = "" ]; then
    if [ "${TRAVIS_BRANCH}" = "master" ]; then
        DOCKER_TAG=${DOCKER_TAG_PREFIX}latest
    else
        DOCKER_TAG=${DOCKER_TAG_PREFIX}${TRAVIS_BRANCH}
    fi
else
    DOCKER_TAG=${DOCKER_TAG_PREFIX}${TRAVIS_TAG}
fi

docker tag ${DOCKER_NAME} ${DOCKER_REPO}:${DOCKER_TAG}

docker login -u="${DOCKER_USERNAME}" -p="${DOCKER_PASSWORD}"

docker push ${DOCKER_REPO}

#docker run -ti ${DOCKER_NAME}
