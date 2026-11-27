# Lipstick

Lipstick publica un servicio que corre en tu máquina (una web, una API, un panel de control, SSH) en un servidor con IP pública, sin abrir puertos en tu red. El cliente se conecta **hacia afuera** al servidor y mantiene esa conexión. Cuando un visitante llega al servidor, el servidor le pide al cliente abrir una conexión inversa, y por ahí pasa el tráfico.

> **Estado: experimental.** Antes de exponer algo a internet, lee [`revision.md`](revision.md): ahí están los puntos de seguridad y estabilidad que quedan abiertos. Para cómo arrancar el sistema paso a paso, ver [`RUN.md`](RUN.md).

## Índice

1. [Qué problema resuelve](#qué-problema-resuelve)
2. [Arquitectura](#arquitectura)
3. [Casos de uso](#casos-de-uso)
4. [Limitaciones](#limitaciones)
5. [Comparación con VPN y proveedores establecidos](#comparación-con-vpn-y-proveedores-establecidos)
6. [Robótica](#robótica)
7. [Requisitos](#requisitos)
8. [Inicio rápido](#inicio-rápido)
9. [Referencia del servidor](#referencia-del-servidor)
10. [Referencia del cliente](#referencia-del-cliente)
11. [Seguridad](#seguridad)
12. [Desarrollo y pruebas](#desarrollo-y-pruebas)

---

## Qué problema resuelve

Tu servicio está detrás de NAT, de un router doméstico, de un 4G o de un firewall corporativo. No puedes abrir puertos entrantes, o no quieres hacerlo. Un servidor con IP pública sí puede recibir conexiones. Lipstick une las dos cosas: el cliente abre la conexión desde tu lado, y el servidor la usa para atender a los visitantes.

```
                   internet                        tu red (sin puertos abiertos)
  visitante ──https──▶ [ servidor lipstick ] ◀──https── [ cliente lipstick ] ──▶ tu servicio
             :5050                              :5051                       127.0.0.1:8082
```

## Arquitectura

### Componentes

| Parte | Dónde corre | Qué hace |
|-------|-------------|----------|
| **Servidor** (`server/`) | Máquina con IP pública | Recibe visitantes, mantiene la conexión de cada cliente y une ambas partes. |
| **Admin API** (`server/admin/`) | Servidor, solo en `127.0.0.1:5052` | Crea y configura dominios, y consulta el tráfico. |
| **Cliente** (`client/`) | Tu máquina, junto al servicio | Mantiene la conexión de control y abre conexiones inversas para cada visitante. |
| **PostgreSQL** | Junto al servidor | Guarda dominios y el tráfico diario. |
| **NATS** | Junto al servidor | Solo para el cluster: cada servidor publica qué dominios tiene con sesiones activas. No transporta tráfico de visitantes. |

### Ciclo de una petición

1. **Registro.** El cliente se conecta a `https://servidor:5051` con la clave del dominio. El servidor valida la clave en tiempo constante y responde `200`. A partir de ahí, la conexión queda abierta como canal de control.
2. **Llega un visitante.** Un navegador conecta a `https://servidor:5050`. El servidor termina el TLS y lee el primer bloque de datos descifrado.
3. **Elección del dominio.** Si los datos son HTTP, el dominio sale del header `Host`. Si no, sale del SNI del handshake TLS. Con ese nombre se elige el **hub** del dominio, que es el objeto que coordina a sus clientes.
4. **Ticket.** El hub genera un ticket aleatorio de 128 bits y lo envía por el canal de control del cliente. La conexión del visitante queda en espera.
5. **Conexión inversa.** El cliente abre `https://servidor:5051/<ticket>`, es decir, una conexión nueva **desde el cliente hacia el servidor**. El servidor empareja esa conexión con la del visitante.
6. **Transferencia.** El cliente reenvía los bytes al servicio local (`-p`). Las respuestas vuelven por el mismo camino.

Si ningún cliente atiende en 30 segundos, el visitante recibe `502`. Cada hub admite como máximo 1024 visitantes en espera.

### Protocolos

- **Hacia afuera (visitantes y clientes):** siempre TLS. No hay modo sin cifrado.
- **Hacia tu servicio local:** `host:puerto` (TCP plano, pensado para `127.0.0.1`) o `https://host:puerto` (TLS verificado). El túnel en sí siempre va cifrado.
- **UDP solo en puertos de intranet.** El tráfico UDP de un visitante no cruza el túnel por el proxy. Solo los puertos de intranet reservados por el cliente lo llevan, sin TLS, como los TCP. Ver [Puertos UDP de intranet](#puertos-udp-de-intranet).

- **Multiplexación.** El cliente abre 8 conexiones TLS autenticadas, y cada una lleva una sesión yamux. Cada visitante es un stream dentro de una sesión, así que no hace falta una conexión TLS nueva por visitante. Un visitante sin sesión disponible usa el camino del ticket, y nada deja de funcionar. Las sesiones se reconectan solas si el servidor se reinicia.

### Un dominio, uno o varios clientes

Cada dominio tiene una clave. Con `allowMultipleConnections: true` puede haber varios clientes conectados para el mismo dominio, y el servidor elige uno al azar para cada visitante. Con `false`, un segundo cliente se rechaza.

### Ciclo de vida

- Un hub se crea con el primer cliente y se detiene cuando se queda sin clientes.
- Un cliente que no envía señales durante 90 segundos se da de baja. Los clientes envían una señal cada 30.
- Si se borra un dominio, o se cambia su clave o su política, el hub se detiene y sus clientes se reconectan con la configuración nueva.
- Si el cliente cae, reintenta con espera creciente: 3, 6, 12 … hasta 60 segundos.

### Estructura del código

```
server/
  main.go            arranque: configuración, TLS, listeners
  config/            YAML y flags, validación, generación de certificados
  manager/           hub por dominio, listener del manager, upgrade, tickets
  admin/             API de administración
  auth/              dominios en PostgreSQL, caché de 5 minutos
  traffic/           contadores de bytes por dominio y día
  db/                conexión y migraciones (GORM)
  subscriptions/     conexión compartida a NATS
client/
  main.go            conexión de control y ciclo de tickets
  manager/           conexión al servidor y conexión inversa
  handlers/          reenvío HTTP, WebSocket y TCP al servicio local
  tlsconf/           política de verificación de certificados
  config/            YAML y flags
helper/              lectura de headers, RemoteConn, utilidades
```

---

## Casos de uso

- **Demo o revisión de un prototipo.** Muestras tu aplicación local a un cliente o a un compañero, sin desplegarla.
- **Webhooks en desarrollo.** Un servicio externo (pagos, mensajería, GitHub) necesita llegar a tu máquina con una URL pública.
- **Acceso a un equipo detrás de NAT.** Un servidor doméstico, una Raspberry Pi o un NAS con una interfaz web o una API.
- **Panel de un dispositivo remoto.** Un equipo en campo (sensor, estación, quiosco) que reporta por HTTP y debe ser administrable desde fuera.
- **Robótica.** Ver la sección [Robótica](#robótica).
- **Laboratorios y formación.** Exponer servicios de práctica sin tocar la red del aula.

### Infraestructura híbrida

Muchas organizaciones tienen recursos **on-premise** (una oficina, una planta, un laboratorio, un almacén) y servicios en la nube, y necesitan que el personal remoto llegue a los equipos locales sin una VPN para cada persona. Lipstick sirve para eso: cada recurso local se publica como un **dominio**, y el acceso se controla con la clave de ese dominio.

```
 nube / personal remoto                  oficina / planta / laboratorio
  impresora-oficina.example.com ─┐
  camara-almacen.example.com  ───┼──▶ [ servidor lipstick ] ◀── cliente (una o varias
  spark-ui.example.com        ───┘                              instancias) ──▶ 192.168.1.50:631
                                                                                 192.168.1.60:80
                                                                                 10.0.5.20:4040
```

#### Qué se puede exponer

| Recurso | Protocolo | Estado | Notas |
|---------|-----------|--------|-------|
| **Impresora de red** | IPP sobre HTTP (puerto 631), la web de administración (HTTP o HTTPS), impresión RAW (9100) | Funciona | IPP y la web de administración son HTTP, así que van por el túnel sin cambios. RAW no tiene TLS ni nombre: usa un puerto reservado con `-intranet-port` (ver [Puertos sin TLS](#puertos-sin-tls-para-la-intranet)). |
| **Cámara IP** | Interfaz web, instantánea JPEG, MJPEG, HLS por HTTP, ONVIF | Funciona | El video en vivo por HTTP funciona mientras la conexión esté abierta. RTSP con RTP usa UDP y **no** pasa por el túnel. |
| **Control de robótica** | HTTP y WebSocket, gRPC, MQTT sobre TLS, Modbus TCP | Funciona para supervisión | El control en tiempo real no es adecuado. Ver [Robótica](#robótica). |
| **Apache Spark** | Interfaz web (4040), envío REST del master (6066), Spark Connect (gRPC, 15002), Livy (REST, 8998) | Funciona cuando el envío es de una sola vía (probado con el envío REST del master) | Las aplicaciones de dos vías no funcionan. Ver la nota sobre Spark abajo. |
| **Notebooks** (Jupyter y similares) | HTTP y WebSocket | Probado con JupyterLab, incluida la ejecución de código por WebSocket | Protege el notebook con contraseña o token. Ver [Servicios probados](#servicios-probados). |
| **Acceso SSH** a equipos locales | TCP | Funciona | Usa claves, no contraseñas. |
| **Bases de datos** | TCP (PostgreSQL 5432, MySQL 3306) | Funciona, con cuidado | Adecuado para administración. No lo uses como canal de datos de alto volumen. |

#### Un subdominio por servicio

Cada servicio se publica con su propio nombre (subdominio), su propia clave y su propio proceso de cliente. Un nombre no debe tener más de un destino: si un mismo cliente pone varios `-p` bajo el mismo dominio, cada destino tiene su propia conexión de control y el servidor elige una al azar para cada visitante. **Un visitante puede llegar a un servicio distinto del que pidió.** Mientras cada servicio tenga su subdominio, no hay conflicto:

```bash
# impresora de la oficina
go run ./client -c /etc/lipstick/impresora.yml     # api_secret de impresora-oficina, proxy_pass 192.168.1.50:631

# cámara del almacén
go run ./client -c /etc/lipstick/camara.yml        # api_secret de camara-almacen, proxy_pass 192.168.1.60:80

# interfaz de Spark del laboratorio
go run ./client -c /etc/lipstick/spark.yml         # api_secret de spark-ui, proxy_pass 10.0.5.20:4040
```

Cada archivo de configuración tiene su propio subdominio y su propia clave. Así también puedes dar de baja un servicio sin afectar a los demás.

#### Servicios probados

Probados a través del túnel, con un servidor y un cliente en la misma máquina. Los servicios corrieron en contenedores Docker o en procesos locales, según el caso.

| Servicio | Camino | Qué se probó | Notas |
|----------|--------|--------------|-------|
| **JupyterLab** | HTTP y WebSocket | API REST, página de lab, y ejecución de código en un kernel Python por WebSocket: salida, resultado y 2000 líneas | Requiere token. |
| **Ollama** | HTTP | `/api/tags`, generación completa y generación en streaming (NDJSON, `done: true`) | Con CPU, una respuesta corta tarda unos 9 segundos con un modelo de 4B parámetros. Lo lento es el modelo, no el túnel. |
| **Apache Spark** (API REST de envío del master) | HTTP | Envío, estado, cancelación, y un job que terminó con el resultado correcto | Solo envío de una vía. Ver la nota sobre Spark abajo. |
| **S3 (SeaweedFS)** | HTTP | Crear bucket, subir y bajar objetos de 1 KiB, 1 MiB y 5 MiB con el mismo SHA-256, listar y borrar | Probado con SeaweedFS porque la imagen de MinIO no estaba disponible aquí. La API S3 es la misma. |
| **Prometheus** | HTTP | `/-/ready`, consulta instantánea (`up`) y consulta de rango con 7 puntos | |
| **Grafana** | HTTP y WebSocket | Health, login, una llamada autenticada a `/api/user`, y conexión a Grafana Live por WebSocket | Requiere `GF_SERVER_ROOT_URL=https://localhost:5050/` (la URL pública). Sin eso, Grafana responde `403 origin not allowed` al WebSocket. |
| **Redis** | TCP por puerto de intranet | `PING`, valor de 2 MiB con el mismo contenido al volver, y 500 `INCR` en pipeline | |
| **SSH** | TCP por puerto de intranet | Comando remoto con clave, y `scp` de 5 MiB en ambos sentidos con el mismo SHA-256 | Probado con OpenSSH en Alpine. El puerto de intranet va sin TLS, así que usa esta vía solo dentro de la red de confianza. |

| **Impresora de red** (emulada) | TCP por puerto de intranet | Trabajo de impresión de 300 KB enviado con el cierre de envío habitual, y guardado idéntico byte a byte | Emulada con `examples/devices/printer_emu.py`. Prueba con una impresora real antes de usarla. |
| **Cámara IP** (emulada) | HTTP por puerto de intranet | Foto fija JPEG, y stream MJPEG con 21 frames en 4 segundos | Emulada con `examples/devices/camera_emu.py`. RTSP no se probó: su video va por UDP, que Lipstick no lleva. |
| **Controlador Modbus TCP** (emulado) | TCP por puerto de intranet | Lectura de 10 registros, escritura y lectura de vuelta, y respuesta de excepción para una dirección inválida | Emulado con `examples/devices/modbus_emu.py`. Para control en tiempo real no es adecuado (ver [Robótica](#robótica)). |

Probados en el cliente (HTTP y TCP genéricos): descargas de hasta 5 MiB, subidas de hasta 1 MiB con el mismo SHA-256 al llegar, keep-alive con 10 peticiones en una conexión, y 20 clientes en paralelo.

Pendiente de probar: bases de datos SQL por puerto de intranet (PostgreSQL, MySQL), servicios con gRPC o MQTT, IPP/CUPS (HTTP, puerto 631), y dispositivos reales en lugar de emulados. Si los usas, pruébalos antes de confiar en ellos.

#### Spark: qué sí y qué no

Probado con un master standalone Spark 3.5.3 y un worker en la misma máquina, a través del túnel:

- **Sí, envío de una sola vía.** Encolar un trabajo en el master por su API REST (puerto 6066, `spark.master.rest.enabled=true`) funciona a través del túnel: el envío, el estado del driver y el cancelado pasan por el proxy. El driver corre en un worker del cluster, así que el cliente nunca necesita recibir conexiones. Un job de suma distribuida terminó en `FINISHED` con el resultado correcto. Las mismas conexiones de una sola vía sirven para Spark Connect (15002) y Livy (8998), que no probé.
- **Sí, `spark-submit` en modo client por un puerto de intranet**, siempre que el cluster pueda llegar al driver. Con el driver en una dirección alcanzable, el job terminó bien a través del puerto de intranet 5101.
- **No, cuando el cluster no puede llegar al driver.** En modo client, los executors tienen que conectarse de vuelta al driver. Con `spark.driver.host` en una dirección que el cluster no alcanza, los executors arrancan pero nunca se registran, y el job se queda en "Initial job has not accepted any resources". Esa conexión de vuelta no pasa por el túnel, así que este caso no se puede resolver con Lipstick. Es el caso de "dos sentidos" de arriba.
- **Requisitos de Java.** Spark 3.5 no arranca con Java 25 (`getSubject is not supported`). Con Java 17 hace falta pasar `--add-opens` al driver y a los executors (`spark.driver.extraJavaOptions` y `spark.executor.extraJavaOptions`). Un worker con poca memoria también bloquea el job: el driver de modo cluster reserva la memoria del worker.
- Los puertos y modos de despliegue cambian entre versiones de Spark. Prueba el flujo concreto con tu versión antes de depender de él.

#### Principio general

**El túnel solo transporta conexiones que inician los visitantes.** Un dispositivo no puede iniciar una conexión hacia fuera a través de Lipstick, y eso afecta a los diseños en los que el equipo "llama a casa" (por ejemplo, una cámara que sube video a la nube). Para esos casos, el dispositivo debe conectarse a un servicio que sí esté publicado, o usa una VPN.

#### Consideraciones de seguridad en infraestructura local

- **Los dispositivos suelen traer contraseñas por defecto y servicios antiguos.** Cámaras e impresoras son objetivos habituales. Cambia las credenciales antes de publicarlos, y desactiva los servicios que no uses.
- **No hay lista blanca de IP por dominio.** Cualquiera que conozca el nombre puede intentar conectar. Restringe el acceso con un firewall delante del servidor, o con autenticación en la propia aplicación.
- **Un dispositivo comprometido está dentro de tu red.** El cliente corre en la red local y puede ser el punto de entrada. Ejecútalo con el mínimo de permisos y en una red segmentada.
- **Revisa qué se expone.** Una interfaz de administración de una impresora no debería ser pública. Publica solo lo que necesitas.

Lipstick no es el mejor punto de partida si necesitas **producción con SLA**, **acceso de muchos usuarios con SSO**, **inspección de tráfico en el borde** o **UDP** (ver [Limitaciones](#limitaciones)).

---

## Limitaciones

### Técnicas

- **UDP solo en intranet.** Un puerto UDP de intranet lleva datagramas unicast, enmarcados sobre TCP, sin latencia garantizada: el túnel sigue siendo TCP por debajo, así que una pérdida retrasa los datagramas que vienen detrás. QUIC, WebRTC con medios y multicast (como el descubrimiento de DDS en ROS 2) quedan fuera.
- **Un servidor por dominio.** La conexión inversa tiene que llegar a la instancia que tiene al visitante en espera. Un balanceador que reparta clientes entre instancias no funciona tal cual.
- **El cliente y el servidor tienen que poder alcanzar el mismo host y puerto.** El servidor no conoce la IP pública del cliente, y el cliente siempre marca el host de la URL que configuró.
- **Latencia y ancho de banda.** Cada byte pasa por el servidor. Un visitante lejano y un servidor lejano suman latencia. El ancho de banda lo limita el servidor.
- **Sin reanudación.** Si el cliente cae en mitad de una transferencia, esa conexión se pierde. Las conexiones nuevas se restablecen solas.
- **Dominio por nombre.** El nombre que usa el visitante (Host o SNI) decide el dominio. No hay rutas por dominio ni enrutamiento por ruta.
- **Certificado del servidor.** Por defecto es autofirmado y solo válido para `localhost`. Para uso público, pon un certificado emitido por una CA reconocida en `tls.certificate_path` y `tls.key_path`. Un visitante puede confiar en él sin configurar nada.

### Operativas

- **Panel sin clave.** El panel de `/` lee datos sin clave, por lo que depende de que el puerto 5052 quede solo en loopback. Las escrituras siguen protegidas con la clave.
- **Sin límites de tasa.** Los visitantes no tienen límite por IP ni por dominio, más allá de los 1024 en espera por hub.
- **Sin alertas ni métricas exportadas.** Hay logs y consulta de tráfico diario, nada más.
- **Un solo punto de fallo.** Si el servidor cae, todos los dominios caen.
- **Se requiere PostgreSQL y NATS** aunque el tráfico no pase por ellos. En un servidor único, NATS solo se usa si configuras un cluster.

### De seguridad (abiertas)

Ver [`revision.md`](revision.md) para la lista completa con su estado. Lo más importante antes de exponer algo:

- La admin API no tiene límite de intentos fallidos. Mantén `admin.address` en `127.0.0.1`.
- Los servicios locales con `tcp://` o `http://` viajan en texto plano dentro de tu máquina.
- La clave del dominio viaja solo dentro del TLS, pero quien la tenga puede conectar un cliente. Trátala como una contraseña y rótala si se filtra.

---

## Comparación con VPN y proveedores establecidos

La comparación es de enfoque, no de calidad. Cada herramienta resuelve un problema distinto. Las características de proveedores cambian con el tiempo: verifica siempre la documentación actual antes de decidir.

### Lipstick frente a una VPN (WireGuard, Tailscale, OpenVPN)

| | Lipstick | VPN |
|---|---|---|
| Qué expone | Servicios concretos (un puerto de un host) a visitantes de internet | Una red completa entre dispositivos autorizados |
| Quién entra | Cualquiera que llegue al servidor público (la clave protege el registro del cliente, no el acceso de los visitantes) | Solo los dispositivos de la VPN |
| Requiere IP pública | En el servidor, sí. En tu red, no | No, si usas un coordinador o relay |
| Protocolo | TCP | IP completo (TCP, UDP, ICMP) |
| Caso típico | Mostrar un servicio a otros | Acceder a tus equipos desde fuera |
| Latencia | Un salto extra por el servidor | Depende de la topología |

**Cuándo elegir una VPN:** para acceder tú o tu equipo a recursos internos, para tráfico UDP o para redes completas. **Cuándo elegir Lipstick:** para que terceros lleguen a un servicio concreto sin tocar la red, por HTTP o TCP.

### Lipstick frente a proveedores establecidos (ngrok, Cloudflare Tunnel y similares)

| | Lipstick | Proveedores establecidos |
|---|---|---|
| Dónde corre el servidor | En tu infraestructura | En la del proveedor |
| Costo | Tu servidor y tu tiempo | Planes gratuitos y de pago |
| Certificados | Los que pongas, o autofirmado | Gestionados por el proveedor |
| Infraestructura global | Un servidor | Red de bordes en varias regiones |
| Autenticación de visitantes (SSO, contraseña por URL) | No incluida | Incluida en varios planes |
| Inspección y reproducción de peticiones | No incluida | Incluida en varios planes |
| Dominios propios | Sí, los que apunten a tu servidor | Sí, según el plan |
| Control total de datos | Sí | Depende del contrato |
| Soporte y SLA | Ninguno | Según el plan |

**Cuándo elegir Lipstick:** si quieres control total de los datos, no quieres depender de un tercero, o necesitas una herramienta mínima que puedas auditar y modificar. **Cuándo elegir un proveedor:** si necesitas alta disponibilidad, autenticación de visitantes, inspección de tráfico o soporte.

### Lipstick frente a herramientas autohospedadas (frp, rathole, un reverse proxy con SSH)

Son alternativas parecidas: también usan una conexión saliente. frp soporta UDP, que Lipstick no tiene; revisa la documentación de cada una para saber qué cubre. Lipstick añade autenticación por dominio, TLS obligatorio, una API de administración y consulta de tráfico. Si necesitas UDP, elige una de estas.

---

## Robótica

Un robot en campo casi nunca tiene IP pública. Suele estar tras un 4G con NAT, una red de obra o un router de un cliente. Lipstick permite que el robot publique una interfaz de operación, un panel de telemetría o una API HTTP sin abrir puertos.

### Qué funciona bien

- **Paneles y APIs HTTP o WebSocket.** Un panel de telemetría, una interfaz de configuración, una API REST del controlador o un stream de estado por WebSocket.
- **SSH de mantenimiento.** Un servicio SSH local expuesto como TCP. Revisa que el acceso esté restringido en el servidor (firewall) y que el usuario tenga clave, no contraseña.
- **Protocolos sobre TCP:** gRPC, MQTT sobre TLS, WebSocket, Modbus TCP para diagnóstico (no para control en tiempo real).
- **Monitoreo y registro.** Logs, métricas y estado del robot por HTTP.

### Qué no conviene hacer con Lipstick

- **Control en tiempo real.** Cada comando pasa por el servidor. La latencia y el jitter de internet no son aceptables para un lazo de control cerrado, como equilibrio, navegación rápida o un brazo a alta velocidad. Usa un enlace directo o una VPN, y deja solo la supervisión a través del túnel.
- **ROS 2 con DDS de forma directa.** DDS usa UDP y multicast para el descubrimiento, y el túnel no los transporta. Opciones: un router de DDS o Zenoh que encapsule el tráfico sobre TCP en un puente explícito, o una VPN que lleve la red de ROS completa.
- **Video en vivo con baja latencia.** WebRTC necesita UDP. Para video, usa un servidor de medios que entregue HLS o MJPEG por HTTP, que sí pasan por el túnel, con la latencia que eso implica.
- **Comandos de seguridad crítica sin autenticación en el robot.** Aunque el túnel esté cifrado, el robot debe validar cada comando: límites, expiración y confirmación. Lipstick no sustituye eso.

### Ejemplo: panel de un robot

En el robot, el servicio local escucha en `127.0.0.1:8080`:

```bash
go run ./client \
  -s https://robots.example.com:5051 \
  -p 127.0.0.1:8080 \
  -k "$(cat /etc/robot/lipstick.key)" \
  -ca /etc/robot/server-ca.pem
```

El operador abre `https://robot-42.example.com:5050/` y llega al panel. Para que el nombre `robot-42.example.com` funcione, el DNS debe apuntar al servidor, y el visitante tiene que enviar ese nombre en el SNI. Un dominio por robot es la forma más simple.

### Recomendaciones de despliegue en robots

- Un dominio y una clave por robot. Rota la clave si se pierde un equipo.
- Pon el cliente bajo un servicio de sistema con reinicio automático.
- Protege el servidor con un firewall: permite `5050` y `5051`, y no expongas `5052`.
- Mide la latencia real del enlace antes de diseñar la interfaz del operador.
- Registra en el robot qué sesiones se abren y cuándo.

---

## Requisitos

- Go 1.25 o superior, para compilar desde el código fuente.
- PostgreSQL, con una base de datos a la que el servidor pueda conectarse. El esquema se crea al arrancar.
- NATS en `nats://localhost:4222` o donde indiques.
- Un certificado y una clave para el servidor. Si no existen, el servidor genera uno autofirmado en `server/certs/`.
- Docker, si quieres usar la imagen (ver [Despliegue](#despliegue)).

## Inicio rápido

Pasos detallados en [`RUN.md`](RUN.md). En resumen:

```bash
./server-run                 # arranca el servidor; imprime el comando del cliente
```

Crea un dominio con la admin API (ver [Referencia](#referencia-del-servidor)) y conecta un cliente:

```bash
go run ./client -s https://localhost:5051 -p 127.0.0.1:8082 -k "$(cat client_key.txt)" -ca server/certs/server.crt
```

Un visitante llega con:

```bash
curl -sk -H "Host: localhost" https://localhost:5050/archivo.txt
```

## Referencia del servidor

### Puertos

| Puerto | Uso | Por defecto |
|--------|-----|-------------|
| `5050` | Visitantes (`proxy.address`), HTTPS | `:5050` |
| `5051` | Clientes (`manager.address`), HTTPS | `:5051` |
| `5052` | Admin API (`admin.address`), HTTPS | `127.0.0.1:5052` |
| según la política | Puertos reservados para la intranet, **sin TLS** (`intranet_ports`) | solo redes permitidas por dominio |

### Archivo de configuración

El servidor lee un YAML (`-c`, por defecto `/etc/lipstick/config.yml`). Parte de [`server/config.yml.example`](server/config.yml.example). Una clave desconocida es un error, para que una errata no pase desapercibida.

| Clave | Por defecto | Descripción |
|-------|-------------|-------------|
| `admin_secret_key` | obligatoria | Clave de la admin API. Al menos 16 caracteres. El valor de ejemplo se rechaza. |
| `proxy.address` | `:5050` | Listener de visitantes. |
| `manager.address` | `:5051` | Listener de clientes. |
| `admin.address` | `127.0.0.1:5052` | Listener de la admin API. |
| `tls.certificate_path` | `certs/server.crt` | Certificado. Se genera si no existe. Las rutas relativas son relativas al archivo de configuración. |
| `tls.key_path` | `certs/server.key` | Clave privada. Se genera si no existe. |
| `database.host` | `localhost` | Host de PostgreSQL. |
| `database.port` | `5432` | Puerto de PostgreSQL. |
| `database.user` | `postgres` | Usuario. |
| `database.password` | vacío | Contraseña. |
| `database.database` | `app_db` | Base de datos. |
| `database.ssl_mode` | `disable` | `disable`, `allow`, `prefer`, `require`, `verify-ca` o `verify-full`. |
| `nats.url` | `nats://localhost:4222` | Servidor NATS. Obligatorio para el cluster, y debe ser `tls://` con credenciales. |
| `nats.username` / `nats.password` | vacío | Credenciales de NATS. Van juntas, y nunca por una conexión `nats://` sin TLS. |
| `nats.token` | vacío | Alternativa a usuario y contraseña. No se combina con ellos. |
| `nats.ca_file` | vacío | CA que firma el certificado del servidor NATS, para `tls://`. |
| `cluster.advertise_address` | desactivado | Dirección `host:puerto` del puerto de manager de este servidor, tal como la alcanzan los otros servidores. Activa el cluster. |
| `cluster.ca_file` | vacío | CA que firma los certificados de los otros servidores. Vacío: se confía en el certificado propio de este servidor. |
| `intranet_ports` | desactivado | Puertos TCP sin TLS, reservados por los clientes dentro de una política. Ver [Puertos sin TLS](#puertos-sin-tls-para-la-intranet). |

### Flags

Los flags reemplazan el valor del archivo cuando los escribes, y se validan igual.

```text
-c                  archivo de configuración (por defecto /etc/lipstick/config.yml)
-admin-addr         admin.address
-manager-addr       manager.address
-proxy-addr         proxy.address
-admin-secret       admin_secret_key
-tls-cert           tls.certificate_path
-tls-key            tls.key_path
-db-host, -db-port, -db-user, -db-password, -db-name, -db-ssl-mode
-nats-url           nats.url
-redis-*            valores de Redis (el servidor todavía no usa Redis)
```

### Admin API

Las rutas de escritura (`POST`, `PATCH`, `DELETE`) requieren la clave de admin en el header `Authorization`, tal cual, sin prefijo `Bearer`. Las de lectura (`GET`) y `/health` no la piden, y solo son accesibles desde loopback.

| Método y ruta | Descripción |
|---------------|-------------|
| `GET /health` | Estado del servicio. Sin autenticación. |
| `GET /domains` | Lista de dominios. No devuelve las claves de los clientes. |
| `GET /domains/:name` | Un dominio. No devuelve la clave. |
| `POST /domains` | Crea un dominio: `{"name", "apiKey", "allowMultipleConnections"}`. |
| `PATCH /domains/:name` | Cambia `apiKey` o `allowMultipleConnections`. Desconecta los clientes del dominio. |
| `DELETE /domains/:name` | Borra el dominio. Desconecta sus clientes. |
| `GET /domains/:name/traffic?from=YYYY-MM-DD&to=YYYY-MM-DD` | Bytes transferidos por día. |
| `GET /stats` | Estado en vivo de cada dominio: estado, clientes, sesiones activas, visitantes en espera, bytes desde el arranque y puertos de intranet. |
| `GET /` | Panel web (`https://127.0.0.1:5052/`) con dominios, conexiones, tráfico y peticiones recientes. No pide clave: los endpoints de lectura son públicos solo dentro del puerto de administración (loopback). `/ui` redirige aquí. |
| `GET /requests?domain=&port=&limit=&from=&to=` | Peticiones HTTP terminadas (dominio, puerto de entrada, método, ruta sin query, estado, duración y tamaño de respuesta), de más reciente a más antigua. `port` filtra por puerto: `5050` es el proxy y los demás son puertos de intranet. Solo se registran peticiones HTTP; las conexiones TCP crudas de intranet no aparecen. Sin cabeceras ni cuerpos. Sin rango de fechas, devuelve las más recientes de memoria. Con `from` y/o `to` (`YYYY-MM-DD` o RFC 3339) consulta PostgreSQL, donde se guardan 7 días. Máximo 500. |

Ejemplo:

```bash
ADMIN=$(sed -n 's/^admin_secret_key: "\(.*\)"/\1/p' server/config.yml)
curl -sk -X POST https://127.0.0.1:5052/domains \
  -H "Authorization: $ADMIN" -H 'Content-Type: application/json' \
  --data '{"name":"robot-42.example.com","apiKey":"<clave-larga-aleatoria>","allowMultipleConnections":false}'
```

## Cluster (varios servidores)

Varios servidores pueden servir los mismos dominios. Todos usan la misma base de datos, el mismo NATS y el mismo certificado (o certificados firmados por la misma CA, indicada en `cluster.ca_file`).

- Cada servidor publica por NATS, cada 5 segundos, los dominios para los que tiene sesiones de cliente. Un servidor que deja de publicar se olvida a los 15 segundos.
- NATS exige credenciales. Un cluster sin credenciales o con `nats://` no arranca. Sin credenciales, NATS rechaza la conexión, y un cliente anónimo no puede leer la presencia (`Authorization Violation`).
- Un visitante que llega a un servidor sin sesión para su dominio se **reenvía** a un servidor que sí la tiene. El reenvío va por una sesión persistente, abierta una vez por par y dominio, sobre una conexión TLS verificada con el certificado del cluster. La sesión se autentica con una prueba hecha con la clave del dominio, y cada visita es un stream dentro de ella.
- El servidor que recibe el reenvío entrega la visita a una de sus sesiones. El puerto de entrada original se conserva en el registro de peticiones.
- Si ningún servidor tiene sesión para el dominio, el visitante recibe `502` de inmediato.

Coste: medido en una misma máquina, un visitante reenviado cuesta casi lo mismo que uno atendido directamente. Con conexión nueva por petición, unas 2 500 peticiones por segundo frente a 2 700 directas (p50 de 4.0 ms frente a 3.6 ms). Con conexiones persistentes, unas 9 700 frente a 10 900.

Pendiente: no se ha probado con servidores en máquinas distintas, ni con caídas de un servidor en mitad de una carga.

## Puertos UDP de intranet

Un cliente puede pedir un puerto UDP además de un puerto TCP, con las mismas reglas: el servidor decide el rango, el dominio y las redes permitidas, y el puerto no tiene TLS.

- Cada origen que envía al puerto (IP y puerto de origen) recibe su propio flujo. Los datagramas viajan enmarcados con una longitud de dos bytes sobre una sesión del cliente, así que sus límites se conservan.
- El cliente reenvía cada flujo a su servicio UDP local, `udp_target`, y devuelve las respuestas al origen que las pidió.
- Un flujo sin tráfico durante 60 segundos se cierra. El siguiente datagrama de ese origen abre uno nuevo.
- Un datagrama de un origen fuera de las redes permitidas se descarta y queda registrado.
- Si ningún cliente atiende el dominio en ese momento, el datagrama se descarta, como ocurre en UDP.

Configuración del cliente:

```yaml
intranet_udp_port: 5109          # puerto UDP que reserva el servidor
udp_target: "127.0.0.1:9109"     # servicio UDP local
```

Medido en una misma máquina, con el servicio UDP de eco: 200 de 200 datagramas de hasta 1 199 bytes volvieron íntegros, y 2 000 datagramas de un origen permitido volvieron íntegros en 0.14 segundos. Un origen fuera de la política no obtuvo respuesta.

Pendiente: no se ha probado un servicio UDP real (DNS, RTP o BACnet), ni la expiración de flujos en mitad de una carga, ni el comportamiento con pérdida de paquetes en la red entre servidor y cliente.

## Puertos sin TLS para la intranet

Algunos servicios no pueden hablar TLS: impresión RAW (9100), protocolos propios o equipos antiguos. Y en TCP en claro no hay SNI ni `Host`, así que el servidor no puede saber a qué dominio va una conexión. La solución es que el **puerto** identifica al servicio: el cliente pide un puerto al conectarse, el servidor lo reserva para ese dominio, y los servicios de la intranet llaman a `servidor:puerto`.

```
  impresora de la oficina ──▶ 10.0.0.10:5105 ──▶ [ servidor ] ══TLS══ [ cliente ] ──▶ 192.168.1.50:9100
  (intranet, sin TLS)        (puerto reservado)                                        (en la oficina)
```

### Quién decide qué

- **El servidor decide la política:** qué interfaz usar, qué rango de puertos existe, qué dominios pueden reservar y desde qué redes se puede conectar a cada uno. Está en `intranet_ports` del archivo de configuración.
- **El cliente solo pide un puerto** dentro de esa política, en la misma conexión autenticada con la clave del dominio. Un cliente no puede elegir redes ni abrir puertos fuera del rango.

```yaml
intranet_ports:
  bind_address: "10.0.0.10"       # una interfaz de la intranet. 0.0.0.0 y :: se rechazan
  port_range: [5100, 5199]         # los puertos que los clientes pueden pedir
  domains:
    - name: "impresora.oficina.example.com"
      allowed_cidrs: ["10.20.0.0/16"]      # redes que pueden conectarse a este dominio
```

```bash
go run ./client -s https://robots.example.com:5051 -p 192.168.1.50:9100 \
  -k "$(cat impresora.key)" -ca server-ca.pem -intranet-port 5105
```

Los servicios de la intranet usan `robots.example.com:5105` (o la IP pública del servidor, según la red) como destino RAW.

### Reglas

- **Opt-in.** Sin `intranet_ports`, el servidor no abre ningún puerto sin TLS.
- **Política obligatoria.** Sin `domains`, o con un dominio sin `allowed_cidrs`, no arranca. Un `allowed_cidrs` con `0.0.0.0/0` o `::/0` se rechaza.
- **Un dominio por puerto.** Un puerto reservado solo sirve a su dominio. Dos dominios no pueden tener el mismo puerto: el segundo recibe `409`.
- **Rechazos explícitos.** Un puerto fuera de rango o de un dominio sin política recibe `403`; un puerto ocupado recibe `409`. El cliente muestra la línea de estado y reintenta con espera creciente.
- **Filtrado al aceptar.** Una conexión de una red no permitida se cierra antes de llegar al túnel, sin respuesta.
- **Liberación automática.** Cuando el dominio se queda sin clientes, sus puertos se cierran. Un cliente que vuelva a conectarse con el mismo puerto lo reserva otra vez.
- **El túnel sigue cifrado.** Entre el servidor y el cliente siempre hay TLS. El tramo sin cifrar es solo el de la intranet hasta el servidor.

### Cómo protegerlo en la nube

La política de Lipstick es la última barrera, no la única. Recomendado:

1. Abre en el grupo de seguridad o el firewall de la nube solo el rango de puertos y solo para las redes de la intranet.
2. Pon `bind_address` en una interfaz privada (VPN o red interna) cuando puedas. Así el puerto ni siquiera existe para internet.
3. Revisa que la IP que ve el servidor sea la de origen real. Si hay un NAT o un proxy delante, la IP de origen será la del NAT, y tienes que listar esa.
4. Vigila los logs: cada reserva, cada rechazo y cada conexión bloqueada queda registrada.

Al arrancar, el servidor avisa cada vez que reserva un puerto sin TLS.

## Referencia del cliente

### Flags

```text
-s   URL del servidor. Debe empezar por https://. Por defecto https://localhost:5051
-p   servicios locales a exponer, host:puerto, separados por espacios. Por defecto 127.0.0.1:8082
-k   clave del dominio (la que creaste en la admin API)
-ca  archivo PEM con el certificado del servidor o de su CA (necesario con certificado autofirmado)
-c   archivo de configuración. Por defecto config.client.yml junto al binario
-intranet-port  puerto sin TLS que pides reservar en el servidor, dentro de su política (solo para servicios de la intranet)
```

### Archivo de configuración

Parte de [`client/config.yml.example`](client/config.yml.example).

| Clave | Descripción |
|-------|-------------|
| `api_secret` | Clave del dominio. Obligatoria. |
| `server_url` | URL del servidor, `https://`. |
| `proxy_pass` | Lista de servicios locales, `host:puerto`. |
| `ca_file` | Archivo PEM que se confía, para un servidor con certificado autofirmado. |
| `intranet_port` | Puerto sin TLS que pides reservar para este dominio. Opcional. |

Los flags reemplazan al archivo. Una clave desconocida es un error.

### Comportamiento

- El cliente siempre verifica el certificado del servidor. No hay forma de desactivarlo.
- Si el servidor rechaza la conexión (por ejemplo, con una clave incorrecta), el cliente muestra el código de estado y reintenta con espera creciente.
- `tcp://` y `http://` hacia el servicio local son TCP plano. `https://` verifica el certificado del servicio local.

## Seguridad

- **TLS obligatorio** hacia visitantes, clientes y admin API. La única excepción son los puertos de `intranet_ports`: opcionales, en una interfaz de la intranet, un dominio por puerto y redes permitidas por la política (ver [Puertos sin TLS](#puertos-sin-tls-para-la-intranet)).
- **Claves de dominio** comparadas en tiempo constante. Una clave vacía nunca autoriza.
- **Tickets** de 128 bits aleatorios, de un solo uso.
- **Sesiones autenticadas.** Cada conexión que el cliente abre hacia el servidor (`GET /<ticket>` o `GET /mux`) lleva una prueba `X-Lipstick-Proof`: HMAC-SHA256 de la clave del dominio. Para un ticket, la prueba cubre el ticket. Para una sesión de multiplexación, cubre un nonce de un solo uso que contiene la hora, y el servidor rechaza un nonce repetido o con más de dos minutos de diferencia con su reloj. La clave nunca viaja en esas conexiones, y una conexión sin prueba válida se cierra sin responder. Dentro de una sesión autenticada, el servidor abre streams y el cliente los acepta: solo el cliente que abrió la sesión recibe tráfico por ella. Un cliente anterior a estos cambios no sirve tráfico: hay que actualizar servidor y clientes juntos.
- **Admin API** solo en loopback por defecto. Las lecturas (`GET`) no piden clave; crear, cambiar o borrar dominios sí, con la clave de admin. Si expones el puerto 5052 fuera de la máquina, cualquiera podrá ver dominios y tráfico.
- **Secretos fuera del repositorio.** `config.yml`, `client_key.txt`, `certs/` y las claves privadas están en `.gitignore`, y `.dockerignore` los excluye de la imagen.
- **Pendiente:** ver [`revision.md`](revision.md). Incluye la clave de un certificado que estuvo en el historial del repositorio y el estado de las referencias de pull request, que GitHub no permite reescribir con un push normal.

## Despliegue

### Servicio de sistema (systemd)

Cada parte tiene su instalador y su desinstalador. Ambos validan la configuración antes de tocar el sistema: si no es válida, no instalan nada.

**Servidor** (desde `server/`, con un `config.yml` listo):

```bash
sudo sh install.sh                      # compila, valida, crea el usuario lipstick y habilita lipstick-server.service
sudo sh uninstall.sh                    # quita el servicio y el binario; conserva config y certificados
sudo sh uninstall.sh --purge            # además borra /etc/lipstick y el usuario
```

**Cliente** (desde `client/`, con un `config.yml` que tenga `api_secret`):

```bash
sudo sh install.sh --ca /ruta/server.crt   # copia la CA a /etc/lipstick-client/ca.pem y la usa
sudo sh uninstall.sh                       # quita el servicio; conserva la clave del dominio
sudo sh uninstall.sh --purge               # además borra /etc/lipstick-client y el usuario
```

Ambos instaladores aceptan `--root DIR` para instalar en un directorio de prueba, sin usuario ni servicio. Úsalo para revisar qué se va a copiar antes de instalar.

Los servicios corren con un usuario propio (`lipstick` y `lipstick-client`), con el sistema de archivos en sólo lectura salvo `/etc/lipstick/certs`.

### Imagen de contenedor

```bash
docker build -t lipstick .
docker run -v /srv/lipstick:/etc/lipstick -p 5050:5050 -p 5051:5051 lipstick
```

`/srv/lipstick` debe contener `config.yml`. Si los certificados no existen, se generan en `/srv/lipstick/certs`, así que ese directorio debe ser escribible por el usuario `65532`. La admin API escucha en `127.0.0.1` dentro del contenedor, así que no es accesible desde el host. Para usarla, entra al contenedor o cambia `admin.address` a `0.0.0.0:5052` bajo tu propio control de acceso.

## Desarrollo y pruebas

```bash
go build ./...
go vet ./...
go test -race ./server/... ./client/... ./helper/...
```

- `server/manager`: hub, listener, upgrade y ciclo de vida.
- `server/config`: validación, flags y generación de certificados.
- `client/tlsconf`: política de verificación.
- `helper`: lectura de headers.

Las pruebas no necesitan PostgreSQL ni NATS. Las comprobaciones de extremo a extremo están descritas en `revision.md`.

## Licencia

Pendiente de definir por el autor del repositorio.
