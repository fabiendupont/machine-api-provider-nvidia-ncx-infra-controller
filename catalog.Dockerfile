FROM quay.io/operator-framework/opm:latest

COPY catalog /configs

LABEL operators.operatorframework.io.index.configs.v1=/configs

ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs"]
