# gofly-mqtt

#### 介绍
golang mqtt服务器，v5版本协议，集群版，目前支持DB集群和直连集群

#### 使用说明

- core 包为核心包
- 项目基础配置在 core/gcfg/config.toml里面
- 以package方式 运行 main.go即可 
`` go run main.go ``
#### 目前支持的方案
##### 1. Mongo集群设计
##### 2. Mysql集群设计
- 新增集群共享订阅数据自动合并方法，简易代码 [cluster/stat/colong/auto_compress_sub/factory.go](https://github.com/huanglishi/gofly-mqttv5/blob/dev-cluster-v1/cluster/stat/colong/auto_compress_sub/factory.go)

##### 3. 静态配置启动

#### 多节点启动
![输入图片说明](https://docapi.goflys.cn/common/uploadfile/get_image?url=resource/uploads/20230224/3c246c6973a17a8809b623aeade4f5b6.png?_t=1677222880 "3c246c6973a17a8809b623aeade4f5b6.png")
##### MQTTX使用
![输入图片说明](https://docapi.goflys.cn/common/uploadfile/get_image?url=resource/uploads/20230224/e082ad8593a87df421490d95b5552b12.png?_t=1677231427 "MQTTX调试端口")

#### 待优化实现
1. ~~发送给客户端的pkid应该专属，不能用上发来的那个旧pkid~~
2. 释放出栈消息的两个阶段可以添加批量删除，不然一个一个删除太慢了
3. ~~session从数据库初始化的消息拉取~~
4. 断线后状态变更，是否需要丢弃内存中那份，每次都从数据库中获取？ 
   
   > --- 当前节点先不删，等到了过期时间再系统自动删除，当节点在其它节点连接时，
        其它节点会通知这边删除旧session，其他节点那边从数据库获取再初始化，
        如果又在自己这个节点上连接，则会继续使用这个session。需要注意qos=0的消息
   
5. ~~断开连接后，需要处理完输入缓冲区内收到的消息~~

6. 消息过期间隔
   
7. ~~session过期间隔，disconnect中可以重新设置过期时间~~

8. Request/Response 模式 ---（客户端处理）

9. ~~订阅标识符~~

10. ~~订阅选项 NoLocal、Retain As Publish、Retain Handling处理~~
    
11. ~~主题别名(Topic Alias)处理~~

12. ~~流控~~

13. Receive Maximum 属性

14. 补充Reason string

15. Maximum Packet Size处理

16. 遗嘱延迟发送处理
    
17. 订阅了共享订阅的客户端掉线后，导致其它节点依旧会发送共享订阅给这个客户端，需要处理，等重连后重新订阅此共享订阅

18. 共享订阅保留问题【mysql方式已解决】
    
19. .....

#### 旧设计思路
![输入图片说明](https://docapi.goflys.cn/common/uploadfile/get_image?url=resource/uploads/20230224/c704927bda1c9f40b9b40850e3747d86.png?_t=1677229905 "客户端消息处理")
![输入图片说明](https://docapi.goflys.cn/common/uploadfile/get_image?url=resource/uploads/20230224/27575a90cdb7ee661f23570b75b28394.png?_t=1677229916 "共享订阅集群通知")

#### 系统领域uml设计
[uml图、不同包中方法调用图](https://github.com/huanglishi/gofly-mqttv5/image)
