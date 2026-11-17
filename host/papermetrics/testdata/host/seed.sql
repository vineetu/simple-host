INSERT INTO paper_daily VALUES ('2026-10-01','accounts',3),('2026-10-01','sites_live',7);
INSERT INTO paper_class_daily VALUES ('2026-10-01','api_read',10,1,1200,300);
INSERT INTO paper_latency_daily VALUES ('2026-10-01','api_read',100,8),('2026-10-01','api_read',250,2);
INSERT INTO paper_process_sample(at,started_at,proc_cpu_cores,proc_rss_bytes,box_cpus) VALUES ('2026-10-01 12:00Z','2026-10-01 00:00Z',0.25,1024,4);
INSERT INTO paper_site_footprint(week,versions,live_files,live_bytes,total_bytes) VALUES ('2026-09-28',2,1,20,40);
INSERT INTO api_request_daily(day,route,status,calls) VALUES ('2026-10-01','GET /v1/sites',200,10);
