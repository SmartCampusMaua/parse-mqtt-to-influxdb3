(
  SELECT time, 'Unificada - Bombeiros' as device, 1 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-7cb8-9ed1-1467f311ebf8'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) UNION ALL (
  SELECT time, 'Unificada - Consumo' as device, 2 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-7904-a84c-3bd0d9e304c7'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) UNION ALL (
  SELECT time, 'Unificada - Reserva' as device, 3 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-75fb-8688-0cf22c21eb49'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Unificada - Cisterna' as device, 4 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019b9ae2-fc84-7396-9ea8-fd2a041b7664'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Bloco H' as device, 5 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bb-7fbf-ba3b-3101febc1129'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Bloco U' as device, 6 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-780a-a53e-0859edf1bf0a'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Reserva U' as device, 7 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-7669-af5c-488132dceadb'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Banco S' as device, 8 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-77de-b058-9d4a78419e1a'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Entrada Campo' as device, 9 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019b9ade-55a0-746e-8d41-b2537631441c'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Entrada DMV' as device, 10 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6fa-0dee-7c6c-b791-6cfec79a3429'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
) 
UNION ALL (
  SELECT time, 'Entrada Bloco U' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6fa-0dee-72bf-9e40-96939916414e'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)


UNION ALL (
  SELECT time, 'Bloco W1' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019be6eb-b5bc-7751-839b-c05990ec0ea6'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
UNION ALL (
  SELECT time, 'Bloco W2' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019f9a98-7236-747e-ae7a-2746fcf05dd1'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
UNION ALL (
  SELECT time, 'Bloco P' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019f9a98-7236-772d-9cb9-009fe752ccf5'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
UNION ALL (
  SELECT time, 'S. Paulo - Cisterna' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019f9a98-7236-7c09-a4db-e283b153ef29'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
UNION ALL (
  SELECT time, 'S. Paulo - Bloco B' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019f9a98-7236-7b88-a6ac-45dd0728d16e'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
UNION ALL (
  SELECT time, 'Bandeirantes' as device, 11 as ordem,
CASE 
  WHEN to_unixtime(now()) - to_unixtime(time) > 1800 THEN '{"color" : "#FF746C","font-size":"25px"}'
  ELSE '{"color" : "rgb(115, 191, 105)","font-size":"25px"}'
END as color
FROM sensor_data
WHERE device_id = '019f9a98-7236-767e-b481-0358681f17a5'
AND time <= now()
AND time >= now() - INTERVAL '2 days'
ORDER BY time desc
LIMIT 1
)
ORDER BY ordem
