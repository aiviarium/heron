import { defineTransport } from "@heron/runtime/apis";

export default defineTransport({
  async send(op, request, ctx) {
    const client = await ctx.resource("grpc_client");
    return client.unary(op.path, request);
  },
});
