#!/bin/sh -e

file ftpserver
ls -lh ftpserver

docker login -u="${DOCKER_USERNAME}" -p="${DOCKER_PASSWORD}"

echo "Docker repo: ${DOCKER_REPO}:${TRAVIS_COMMIT}"

DOCKER_NAME=${DOCKER_REPO}:${TRAVIS_COMMIT}

docker build -t ${DOCKER_NAME} .

docker tag ${DOCKER_NAME} ${DOCKER_REPO}:travis-${TRAVIS_BUILD_NUMBER}

if [ "${TRAVIS_TAG}" = "" ]; then
    if [ "${TRAVIS_BRANCH}" = "master" ]; then
        DOCKER_TAG=latest
    else
        DOCKER_TAG=${TRAVIS_BRANCH}
    fi
else
    DOCKER_TAG=${TRAVIS_TAG}
fi

docker tag ${DOCKER_NAME} ${DOCKER_REPO}:${DOCKER_TAG}

docker push ${DOCKER_REPO}
