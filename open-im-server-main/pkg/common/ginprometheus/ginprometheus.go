package ginprometheus

//import (
//	"bytes"
//	"fmt"
//	"io"
//	"net/http"
//	"os"
//	"strconv"
//	"time"
//
//	"github.com/gin-gonic/gin"
//	"github.com/prometheus/client_golang/prometheus"
//	"github.com/prometheus/client_golang/prometheus/promhttp"
//)
//
//var defaultMetricPath = "/metrics"
//
//// counter, counter_vec, gauge, gauge_vec,
//// histogram, histogram_vec, summary, summary_vec.
//var (
//	reqCounter = &Metric{
//		ID:          "reqCnt",
//		Name:        "requests_total",
//		Description: "How many HTTP requests processed, partitioned by status code and HTTP method.",
//		Type:        "counter_vec",
//		Args:        []string{"code", "method", "handler", "host", "url"}}
//
//	reqDuration = &Metric{
//		ID:          "reqDur",
//		Name:        "request_duration_seconds",
//		Description: "The HTTP request latencies in seconds.",
//		Type:        "histogram_vec",
//		Args:        []string{"code", "method", "url"},
//	}
//
//	resSize = &Metric{
//		ID:          "resSz",
//		Name:        "response_size_bytes",
//		Description: "The HTTP response sizes in bytes.",
//		Type:        "summary"}
//
//	reqSize = &Metric{
//		ID:          "reqSz",
//		Name:        "request_size_bytes",
//		Description: "The HTTP request sizes in bytes.",
//		Type:        "summary"}
//
//	standardMetrics = []*Metric{
//		reqCounter,
//		reqDuration,
//		resSize,
//		reqSize,
//	}
//)
//
///*
//RequestCounterURLLabelMappingFn 是一个可传给中间件的函数，用于控制
//请求计数器 "url" 标签的基数（cardinality），在某些场景下这是必需的。
//例如，对于 "/customer/:name" 这类路由，如果不希望为每个可能的客户名都生成一条时间序列，
//可以使用下面这个函数：
//
//	func(c *gin.Context) string {
//		url := c.Request.URL.Path
//		for _, p := range c.Params {
//			if p.Key == "name" {
//				url = strings.Replace(url, p.Value, ":name", 1)
//				break
//			}
//		}
//		return url
//	}
//
//这样就会把 "/customer/alice" 和 "/customer/bob" 都映射到模板 "/customer/:name"。
//*/
//type RequestCounterURLLabelMappingFn func(c *gin.Context) string
//
//// Metric 定义了每个指标的 name、description、type、ID，
//// 以及该指标对应的 prometheus.Collector 类型（如 CounterVec、Summary 等）。
//type Metric struct {
//	MetricCollector prometheus.Collector
//	ID              string
//	Name            string
//	Description     string
//	Type            string
//	Args            []string
//}
//
//// Prometheus 保存本实例采集到的指标以及指标暴露路径。
//type Prometheus struct {
//	reqCnt        *prometheus.CounterVec
//	reqDur        *prometheus.HistogramVec
//	reqSz, resSz  prometheus.Summary
//	router        *gin.Engine
//	listenAddress string
//	Ppg           PrometheusPushGateway
//
//	MetricsList []*Metric
//	MetricsPath string
//
//	ReqCntURLLabelMappingFn RequestCounterURLLabelMappingFn
//
//	// 作为 prometheus URL 标签使用的 gin.Context 字符串
//	URLLabelFromContext string
//}
//
//// PrometheusPushGateway 保存推送到 Prometheus pushgateway 的配置（可选）。
//type PrometheusPushGateway struct {
//
//	// 推送间隔，单位秒
//	PushIntervalSeconds time.Duration
//
//	// Push Gateway 地址，格式为 http://domain:port
//	// 其中 JOBNAME 可以是任意自定义字符串
//	PushGatewayURL string
//
//	// 本地指标地址，指标从这里抓取；如果将来改用 prometheus 的 common/expfmt 实现，
//	// 这个地址可以省略
//	MetricsURL string
//
//	// pushgateway 的 job 名称，默认为 "gin"
//	Job string
//}
//
//// NewPrometheus 以指定的子系统名生成一组新的指标。
//func NewPrometheus(subsystem string, customMetricsList ...[]*Metric) *Prometheus {
//	if subsystem == "" {
//		subsystem = "app"
//	}
//
//	var metricsList []*Metric
//
//	if len(customMetricsList) > 1 {
//		panic("Too many args. NewPrometheus( string, <optional []*Metric> ).")
//	} else if len(customMetricsList) == 1 {
//		metricsList = customMetricsList[0]
//	}
//	metricsList = append(metricsList, standardMetrics...)
//
//	p := &Prometheus{
//		MetricsList: metricsList,
//		MetricsPath: defaultMetricPath,
//		ReqCntURLLabelMappingFn: func(c *gin.Context) string {
//			return c.FullPath() // e.g. /user/:id , /user/:id/info
//		},
//	}
//
//	p.registerMetrics(subsystem)
//
//	return p
//}
//
//// SetPushGateway 每隔 pushIntervalSeconds 秒，把指标推送到
//// 由 pushGatewayURL 暴露的远程 pushgateway；指标从 metricsURL 抓取。
//func (p *Prometheus) SetPushGateway(pushGatewayURL, metricsURL string, pushIntervalSeconds time.Duration) {
//	p.Ppg.PushGatewayURL = pushGatewayURL
//	p.Ppg.MetricsURL = metricsURL
//	p.Ppg.PushIntervalSeconds = pushIntervalSeconds
//	p.startPushTicker()
//}
//
//// SetPushGatewayJob 设置 job 名称，默认为 "gin"。
//func (p *Prometheus) SetPushGatewayJob(j string) {
//	p.Ppg.Job = j
//}
//
//// SetListenAddress 设置指标暴露的监听地址；如果不设置，
//// 则使用当前 gin 引擎的同一地址。
//func (p *Prometheus) SetListenAddress(address string) {
//	p.listenAddress = address
//	if p.listenAddress != "" {
//		p.router = gin.Default()
//	}
//}
//
//// SetListenAddressWithRouter 使用独立路由暴露指标（这样可以把 GET /metrics 这类请求
//// 排除在业务访问日志之外）。
//func (p *Prometheus) SetListenAddressWithRouter(listenAddress string, r *gin.Engine) {
//	p.listenAddress = listenAddress
//	if len(p.listenAddress) > 0 {
//		p.router = r
//	}
//}
//
//// SetMetricsPath 设置指标路径。
//func (p *Prometheus) SetMetricsPath(e *gin.Engine) error {
//
//	if p.listenAddress != "" {
//		p.router.GET(p.MetricsPath, prometheusHandler())
//		return p.runServer()
//	} else {
//		e.GET(p.MetricsPath, prometheusHandler())
//		return nil
//	}
//}
//
//// SetMetricsPathWithAuth 设置带鉴权的指标路径。
//func (p *Prometheus) SetMetricsPathWithAuth(e *gin.Engine, accounts gin.Accounts) error {
//
//	if p.listenAddress != "" {
//		p.router.GET(p.MetricsPath, gin.BasicAuth(accounts), prometheusHandler())
//		return p.runServer()
//	} else {
//		e.GET(p.MetricsPath, gin.BasicAuth(accounts), prometheusHandler())
//		return nil
//	}
//
//}
//
//func (p *Prometheus) runServer() error {
//	return p.router.Run(p.listenAddress)
//}
//
//func (p *Prometheus) getMetrics() []byte {
//	response, err := http.Get(p.Ppg.MetricsURL)
//	if err != nil {
//		return nil
//	}
//
//	defer response.Body.Close()
//
//	body, _ := io.ReadAll(response.Body)
//	return body
//}
//
//var hostname, _ = os.Hostname()
//
//func (p *Prometheus) getPushGatewayURL() string {
//	if p.Ppg.Job == "" {
//		p.Ppg.Job = "gin"
//	}
//	return p.Ppg.PushGatewayURL + "/metrics/job/" + p.Ppg.Job + "/instance/" + hostname
//}
//
//func (p *Prometheus) sendMetricsToPushGateway(metrics []byte) {
//	req, err := http.NewRequest("POST", p.getPushGatewayURL(), bytes.NewBuffer(metrics))
//	if err != nil {
//		return
//	}
//
//	client := &http.Client{}
//	resp, err := client.Do(req)
//	if err != nil {
//		fmt.Println("Error sending to push gateway error:", err.Error())
//	}
//
//	resp.Body.Close()
//}
//
//func (p *Prometheus) startPushTicker() {
//	ticker := time.NewTicker(time.Second * p.Ppg.PushIntervalSeconds)
//	go func() {
//		for range ticker.C {
//			p.sendMetricsToPushGateway(p.getMetrics())
//		}
//	}()
//}
//
//// NewMetric 依据 Metric.Type 关联对应的 prometheus.Collector。
//func NewMetric(m *Metric, subsystem string) prometheus.Collector {
//	var metric prometheus.Collector
//	switch m.Type {
//	case "counter_vec":
//		metric = prometheus.NewCounterVec(
//			prometheus.CounterOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//			m.Args,
//		)
//	case "counter":
//		metric = prometheus.NewCounter(
//			prometheus.CounterOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//		)
//	case "gauge_vec":
//		metric = prometheus.NewGaugeVec(
//			prometheus.GaugeOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//			m.Args,
//		)
//	case "gauge":
//		metric = prometheus.NewGauge(
//			prometheus.GaugeOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//		)
//	case "histogram_vec":
//		metric = prometheus.NewHistogramVec(
//			prometheus.HistogramOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//			m.Args,
//		)
//	case "histogram":
//		metric = prometheus.NewHistogram(
//			prometheus.HistogramOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//		)
//	case "summary_vec":
//		metric = prometheus.NewSummaryVec(
//			prometheus.SummaryOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//			m.Args,
//		)
//	case "summary":
//		metric = prometheus.NewSummary(
//			prometheus.SummaryOpts{
//				Subsystem: subsystem,
//				Name:      m.Name,
//				Help:      m.Description,
//			},
//		)
//	}
//	return metric
//}
//
//func (p *Prometheus) registerMetrics(subsystem string) {
//	for _, metricDef := range p.MetricsList {
//		metric := NewMetric(metricDef, subsystem)
//		if err := prometheus.Register(metric); err != nil {
//			fmt.Println("could not be registered in Prometheus,metricDef.Name:", metricDef.Name, "   error:", err.Error())
//		}
//
//		switch metricDef {
//		case reqCounter:
//			p.reqCnt = metric.(*prometheus.CounterVec)
//		case reqDuration:
//			p.reqDur = metric.(*prometheus.HistogramVec)
//		case resSize:
//			p.resSz = metric.(prometheus.Summary)
//		case reqSize:
//			p.reqSz = metric.(prometheus.Summary)
//		}
//		metricDef.MetricCollector = metric
//	}
//}
//
//// Use 把该中间件注册到 gin 引擎。
//func (p *Prometheus) Use(e *gin.Engine) error {
//	e.Use(p.HandlerFunc())
//	return p.SetMetricsPath(e)
//}
//
//// UseWithAuth 把带 BasicAuth 的中间件注册到 gin 引擎。
//func (p *Prometheus) UseWithAuth(e *gin.Engine, accounts gin.Accounts) error {
//	e.Use(p.HandlerFunc())
//	return p.SetMetricsPathWithAuth(e, accounts)
//}
//
//// HandlerFunc 定义中间件使用的处理函数。
//func (p *Prometheus) HandlerFunc() gin.HandlerFunc {
//	return func(c *gin.Context) {
//		if c.Request.URL.Path == p.MetricsPath {
//			c.Next()
//			return
//		}
//
//		start := time.Now()
//		reqSz := computeApproximateRequestSize(c.Request)
//
//		c.Next()
//
//		status := strconv.Itoa(c.Writer.Status())
//		elapsed := float64(time.Since(start)) / float64(time.Second)
//		resSz := float64(c.Writer.Size())
//
//		url := p.ReqCntURLLabelMappingFn(c)
//		if len(p.URLLabelFromContext) > 0 {
//			u, found := c.Get(p.URLLabelFromContext)
//			if !found {
//				u = "unknown"
//			}
//			url = u.(string)
//		}
//		p.reqDur.WithLabelValues(status, c.Request.Method, url).Observe(elapsed)
//		p.reqCnt.WithLabelValues(status, c.Request.Method, c.HandlerName(), c.Request.Host, url).Inc()
//		p.reqSz.Observe(float64(reqSz))
//		p.resSz.Observe(resSz)
//	}
//}
//
//func prometheusHandler() gin.HandlerFunc {
//	h := promhttp.Handler()
//	return func(c *gin.Context) {
//		h.ServeHTTP(c.Writer, c.Request)
//	}
//}
//
//func computeApproximateRequestSize(r *http.Request) int {
//	var s int
//	if r.URL != nil {
//		s = len(r.URL.Path)
//	}
//
//	s += len(r.Method)
//	s += len(r.Proto)
//	for name, values := range r.Header {
//		s += len(name)
//		for _, value := range values {
//			s += len(value)
//		}
//	}
//	s += len(r.Host)
//
//	// 这里假定 r.FormData 和 r.MultipartForm 已包含在 r.URL 中。
//
//	if r.ContentLength != -1 {
//		s += int(r.ContentLength)
//	}
//	return s
//}
