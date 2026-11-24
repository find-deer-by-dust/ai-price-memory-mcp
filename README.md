# ai-price-memory-mcp

用 Go 封装 AI Price Memory 报价目录。服务端通过产品 slug、同款指纹、币种、报价 ID 和快照 ID 关联公开数据，向 MCP 客户端返回整理后的商品、商家报价和历史趋势。支持 stdio 和 Streamable HTTP。 [PriceMemo 开发者资料](https://ai.pricememo.cn/developers) · [上游路由源码](https://github.com/BeterXie/ai_price_radar/blob/main/apps/api/app/routers/public.py)

## Docker

```sh
docker build -t ai-price-memory-mcp .
```

stdio 是默认传输方式。将下面的配置加入 MCP 客户端。

```json
{
  "mcpServers": {
    "pricememo": {
      "command": "docker",
      "args": ["run", "--rm", "-i", "ai-price-memory-mcp"]
    }
  }
}
```

如果客户端使用 Streamable HTTP，启动容器并连接 `http://127.0.0.1:8080/mcp`。

```sh
docker run -d \
  --name ai-price-memory-mcp \
  -p 127.0.0.1:8080:8080 \
  --restart unless-stopped \
  ai-price-memory-mcp \
  -transport http \
  -listen :8080
```

## 本地运行

需要 Go 1.25 或更高版本。默认上游是 `https://ai.pricememo.cn`，可用环境变量 `PRICEMEMO_BASE_URL` 指定自部署站点。

默认使用 stdio。

```sh
go run .
```

Streamable HTTP 默认监听 `127.0.0.1:8080`，客户端连接 `http://127.0.0.1:8080/mcp`。

```sh
go run . -transport http
```

用 `-listen <地址>:<端口>` 可以指定监听地址和端口。

```sh
go run . -transport http -listen 0.0.0.0:9090
```

运行测试请执行 `go test ./...`。

## MCP 工具

| 工具 | 用途 |
| --- | --- |
| `pricememo_search_products` | 按标准名称或 slug 搜索，精确匹配优先，返回商品标识、库存、近期价格和官方参考。 |
| `pricememo_compare_offers` | 可直接用套餐名或 slug 比价。默认查询有货可比报价，返回同款分组和官方参考，并自动展开第一组的同币种商家。 |
| `pricememo_get_offer_details` | 按报价 ID 合并报价字段与商家原文。原文最多返回 1600 个字符，截断时标记。 |
| `pricememo_get_price_history` | 按币种汇总上游近 90 天的历史，返回历史最低可信观测及最近的日数据。 |

例如，用 `{"product":"ChatGPT Plus","limit":5}` 一次完成识别套餐和比价。名称存在多个匹配时返回候选 slug 供选择。比价结果包含交付形态、服务期限、质保、来源链接和观测时间。展开的商家报价按库存状态和数值价格排序，同一店铺保留排在最前的报价。官方参考保留原币种与计费周期。

翻页时保留原筛选条件，将 `next_offset` 和 `snapshot_id` 分别传给 `offset` 和 `snapshot`。查看指定组的商家时传入该组的 `fingerprint` 和 `price_currency`，后者对应入参 `currency`。报价详情可以使用代表报价或商家报价的 `id`。搜索与比价默认返回 5 条，最多 20 条。
