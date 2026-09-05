// CloudNodeOperation is a power operation this desktop has asked a cloud node
// to perform. It is the discriminator every surface that reports in-flight work
// on a node must carry, rather than re-inferring the verb from the node's
// current state: state and operation disagree precisely while the operation is
// running, which is the only moment a progressive label is shown.
export type CloudNodeOperation = 'start' | 'stop';
