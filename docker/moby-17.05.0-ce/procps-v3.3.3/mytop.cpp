#include<stdio.h>
#include<stdlib.h>
#include<string.h>
#include <string>
#include<unistd.h>
#include<fcntl.h>
#include<ctype.h>
#include <sys/types.h>   
#include <dirent.h>
#include <errno.h> 
#include <vector>
#include <assert.h>
#include <sys/types.h> 
#include <sys/stat.h>

#include<iostream>
using namespace std;

#define CK_TIME 1
//http://www.cnblogs.com/wajika/p/6725723.html  如何统计进程CPU利用率
struct FileAttribute
{
    string path;
    string name;
    unsigned long long size;
    time_t  modify_timestamp;
    bool    is_dir;
};

int EnumFile(vector<FileAttribute> &file_array, string _dir)
{
    DIR* dir=opendir(_dir.c_str());  //(".")
    if(dir == NULL)
        return 0;

    struct dirent* entry;
    while((entry=readdir(dir)))
    {
        if( strcmp( entry->d_name,".") ==0 || strcmp( entry->d_name,"..") ==0 )
            continue;
        FileAttribute fi;
        fi.name = entry->d_name;
        fi.is_dir = false;
        string path;

        if(_dir=="/"||(_dir.rfind("/")+1)>=_dir.length())
            path=_dir+fi.name;
        else
            path = _dir+"/"+fi.name;
        struct stat statbuf;
        if (stat( path.c_str(),&statbuf ) < 0)
        {
            closedir(dir);
            printf("stat error ! message: %s\n",strerror(errno));
            return 0;
        }

        if (S_ISDIR(statbuf.st_mode))
        {
            fi.is_dir = true;
        }
        fi.size = statbuf.st_size;
        fi.modify_timestamp =statbuf.st_mtime;
        fi.path = path;
        file_array.push_back(fi);                    
    }
    closedir(dir);
    return file_array.size();
}

char * skip_token(const char *p)
{
    while (isspace(*p)) p++;
    while (*p && !isspace(*p)) p++;
    return (char *)p;
}

int get_sys_mem(char *mem)
{
    int tm,fm,bm,cm,ts,fs;
    char buffer[4096+1];
    char sys_mem[1024];
    int fd, len;
    char *p;
    //fd = open("/proc/meminfo", O_RDONLY);
    if((fd = open("/proc/meminfo", O_RDONLY)) < 0)
    {
        perror("open /proc/meminfo file failed");
        exit(1);
    }
    len = read(fd, buffer, sizeof(buffer)-1);
    close(fd);
    
    buffer[len] = '\0';
    p = buffer;
    p = skip_token(p); 
    tm = strtoul(p, &p, 10); /* total memory */
    
    p = strchr(p, '\n');
    p = skip_token(p);
    fm= strtoul(p, &p, 10); /* free memory */

    p = strchr(p, '\n');
    p = skip_token(p);
    bm= strtoul(p, &p, 10); /* buffer memory */
    
    p = strchr(p, '\n');
    p = skip_token(p);
    cm= strtoul(p, &p, 10); /* cached memory */
   
    for(int i = 0; i< 8 ;i++) 
    {
        p++;
        p = strchr(p, '\n');
    }
    p = skip_token(p);
    ts= strtoul(p, &p, 10); /* total swap */
        
    p = strchr(p, '\n');
    p = skip_token(p);
    fs= strtoul(p, &p, 10); /* free swap */
    
    sprintf(mem,"Mem: %luk total,%luk used,%luk free,%luk buffer\nSwap: %luk total,%luk used, %luk  free,%luk cached\n",
            tm,tm-fm,fm,bm,ts,ts-fs,fs,cm);
    //printf("%s\n",mem);
    return tm;
}

int get_phy_mem(pid_t pid,char* ph)
{
    char file[64] = {0};//文件名
    FILE *fd;         //定义文件指针fd
    char line_buff[256] = {0};  //读取行的缓冲区
    sprintf(file,"/proc/%d/status",pid);

    //fd = fopen (file, "r");
    if((fd = fopen (file, "r"))==NULL)
    {
        printf("Can't open file\n");
        exit(1);
    }

    //获取vmrss:实际物理内存占用
    int i;
    char name1[32];//存放项目名称
    int vmrss;//存放内存峰值大小
    char name2[32];
    int vmsize;
    for (i=0;i<12;i++)
    {
        fgets (line_buff, sizeof(line_buff), fd);
    }
    fgets (line_buff, sizeof(line_buff), fd);
    sscanf (line_buff, "%s %d", name2,&vmsize);
    //fprintf (stderr, "====%s：%d====\n", name2,vmsize);

    for (i=0;i<2;i++)
    {
         fgets (line_buff, sizeof(line_buff), fd);
    }

    fgets (line_buff, sizeof(line_buff), fd);//读取VmRSS这一行的数据,VmRSS在第15行
    sscanf (line_buff, "%s %d", name1,&vmrss);
    
    //fprintf (stderr, "====%s：%d====\n", name1,vmrss);
    
    fclose(fd);     //关闭文件fd
    sprintf(ph,"VIRT=%dKB RES=%dKB",vmsize,vmrss);
    //printf("=+=+=%s\n",ph);
    return vmrss;
}

int get_process_time(pid_t pid,int tid)
{    
    char szStatStr[1024];
    char pname[64];
    char state;
    int ppid,pgrp,session,tty,tpgid;
    unsigned int    flags,minflt,cminflt,majflt,cmajflt;
    int utime,stime,cutime,cstime,counter,priority;
    unsigned int  timeout,itrealvalue;
    int           starttime;
    unsigned int  vsize,rss,rlim,startcode,endcode,startstack,kstkesp,kstkeip;
    int signal,blocked,sigignore,sigcatch;
    unsigned int  wchan;

    char file_stat [1024];
    if(tid==0)
    {
        sprintf( file_stat,"/proc/%d/stat",pid );
    }else if(tid!=-1) 
    {
        sprintf( file_stat,"/proc/%d/task/%d/stat",pid,tid );
    }
    
    //printf("open file %s\n",file_stat);
   
    FILE* fid;
    //fid = fopen(file_stat,"r");
    if((fid = fopen (file_stat, "r"))==NULL)
    {
        printf("Can't open file\n");
        exit(1);
    }

    fgets(szStatStr,sizeof(szStatStr),fid);
    
    fclose(fid);
    
    //printf("+++szStatStr=%s\n",szStatStr);
    
    sscanf (szStatStr, "%u", &pid);
    char  *sp, *t;
    sp = strchr (szStatStr, '(') + 1;
    t = strchr (szStatStr, ')');
    strncpy (pname, sp, t - sp);

    sscanf (t + 2, "%c %d %d %d %d %d %u %u %u %u %u %d %d %d %d %d %d %u %u %d %u %u %u %u %u %u %u %u %d %d %d %d %u",
             /*     1  2  3  4  5  6  7  8  9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33*/
                    &state,&ppid,&pgrp,&session,&tty,&tpgid,&flags,&minflt,&cminflt,&majflt,&cmajflt,&utime,&stime,&cutime,&cstime,&counter,
                    &priority,&timeout,&itrealvalue,&starttime,&vsize,&rss,&rlim,&startcode,&endcode,&startstack,
                    &kstkesp,&kstkeip,&signal,&blocked,&sigignore,&sigcatch,&wchan);
    /*printf("-------%c %d %d %d %d %d %u %u %u %u %u -%d -%d -%d -%d %d %d %u %u %d %u %u %u %u %u %u %u %u %d %d %d %d %u",
                    state,ppid,pgrp,session,tty,tpgid,flags,minflt,cminflt,majflt,cmajflt,utime,stime,cutime,cstime,counter,
                    priority,timeout,itrealvalue,starttime,vsize,rss,rlim,startcode,endcode,startstack,
                    kstkesp,kstkeip,signal,blocked,sigignore,sigcatch,wchan);
    */
    //printf("+++%lu %lu %lu %lu\n",utime,stime,cutime,cstime);
             
    int p_cpu=utime+stime+cutime+cstime;
    return p_cpu;
}

string GetCpuMem( size_t pid, string &cpu ,string &mem,int tid=0 )
{
    FILE *fp;
    char buf[128];
    char tcpu[7];
    char result[512];
    char ccpu[256];
    char cmem[256];
    char process[1028];

    unsigned int  user,nice,sys,idle,iowait,irq,softirq,steal;
    
    unsigned int  all1,all2;
    
    unsigned int   us1,ni1,sy1,id1,io1,ir1,so1,st1;
    unsigned int   us2,ni2,sy2,id2,io2,ir2,so2,st2;

    unsigned int  p_cpu1,p_cpu2;
    
    float usage,niage,syage,idage,ioage,irage,soage,stage;

    //fp = fopen("/proc/stat","r");
    if((fp = fopen ("/proc/stat", "r"))==NULL)
    {
        printf("Can't open file\n");
        exit(1);
    }
    
    fgets(buf,sizeof(buf),fp);

	printf("yang test...,buf:%s\r\n", buf);
    sscanf(buf,"%s%d%d%d%d%d%d%d%d",tcpu,&user,&nice,&sys,&idle,&iowait,&irq,&softirq,&steal);
    
    //printf("%s,%d,%d,%d,%d,%d,%d,%d,%d\n",tcpu,user,nice,sys,idle,iowait,irq,softirq,steal);

    all1 = user+nice+sys+idle+iowait+irq+softirq+steal; //总时间
    
    us1=user;ni1=nice;sy1=sys;id1=idle;
    io1=iowait;ir1=irq;so1=softirq;st1=steal;
//=============================================

    char file_dir[256];
    sprintf(file_dir,"/proc/%d/task",pid);
    string _dir=file_dir;
    //printf("-----%s\n",file_dir);
    vector<FileAttribute> file_array;
    int file_sum=EnumFile(file_array,_dir);
    unsigned int a[file_sum];
    unsigned int b[file_sum];
    unsigned int b2[file_sum];
    int i=0;

    if(tid==-1)
    {
        for(vector<FileAttribute>::iterator it=file_array.begin();it!=file_array.end();it++)
        {
            _dir=(*it).name;
            //cout<<"_dir="<<_dir<<endl;
            a[i]=atoi( _dir.c_str());
            i++;
        }
        for(int j=0;j<file_sum;j++)
        {
            b[j]=get_process_time( pid,a[j]);

        }
    }else
    {
        p_cpu1= get_process_time( pid, tid);
    }
//===========================================
    /*第二次取数据*/
    sleep(CK_TIME);
    rewind(fp);

    memset(buf,0,sizeof(buf));

    tcpu[0] = '\0';
    user=nice=sys=idle=iowait=irq=softirq=steal=0;

    fgets(buf,sizeof(buf),fp);

    sscanf(buf,"%s%d%d%d%d%d%d%d%d",tcpu,&user,&nice,&sys,&idle,&iowait,&irq,&softirq,&steal);

    //printf("%s,%d,%d,%d,%d,%d,%d,%d,%d\n",tcpu,user,nice,sys,idle,iowait,irq,softirq,steal);
    
    us2=user;ni2=nice;sy2=sys;id2=idle;
    io2=iowait;ir2=irq;so2=softirq;st2=steal;
    all2 = user+nice+sys+idle+iowait+irq+softirq+steal;

    usage =(float)((us2-us1)+(ni2-ni1))/(all2-all1)*100 ;
    syage=(float)((sy2-sy1)+(ir2-ir1)+(so2-so1))/(all2-all1)*100 ;

    idage=(float)(id2-id1)/(all2-all1)*100;
    niage=(float)(ni2-ni1)/(all2-all1)*100;
    ioage=(float)(io2-io1)/(all2-all1)*100;
    irage=(float)(ir2-ir1)/(all2-all1)*100;
    soage=(float)(so2-so1)/(all2-all1)*100;
    stage=(float)(so2-so1)/(all2-all1)*100;

    if(tid==-1)
    {
        for(int j=0;j<file_sum;j++)
        {
            b2[j]=get_process_time( pid,a[j]);
        }
    }else
    {
        p_cpu2= get_process_time( pid, tid);
    }

    int NUM_PROCS = sysconf(_SC_NPROCESSORS_CONF);
    //printf("======%d",NUM_PROCS);

    float prcpu[file_sum];
    float pcpu;
    if(tid==-1)
    {
        for(int j=0;j<file_sum;j++)
        {
            prcpu[j]=(float)(b2[j]-b[j])/(all2-all1)*NUM_PROCS*100;
        }
    }else
    {
        pcpu = (float)(p_cpu2 - p_cpu1)/(all2-all1)*NUM_PROCS*100;
    }

    //printf("cpu(s): %.2f\% ,%.2f\% ,%.2f\% ,%.2f\% ,%.2f\% ,%.2f\% ,%.2f\% ,%.2f\% \n",
    //          usage,syage,niage,idage,ioage,irage,soage,stage);
    sprintf(ccpu,"Cpu(s):  %.2f%%us,%.2f%%sy,%.2f%%ni,%.2f%%id,%.2f%%wa,%.2f%%hi,%.2f%%si,%.2f%%st\n",
            usage,syage,niage,idage,ioage,irage,soage,stage); 
    
    fclose(fp);
    
    char ph[256];
    long page_size = sysconf(_SC_PAGESIZE)>>10;
    float pmem=(get_phy_mem(pid,ph)*page_size)/get_sys_mem(cmem)*100;

    cpu=ccpu;
    mem=cmem;
    
    if(tid==-1)
    {
        int offset = 0;

        for(int j=0;j<file_sum;j++)
        {
            //printf("PID=%d  TID=%d  %.2f%%CPU  %.2f%%MEM %s\n",pid,a[j],prcpu[j],pmem,ph);
           offset += sprintf(process+ offset,"PID=%d  TID=%d  %.2f%%CPU  %.2f%%MEM %s\n",pid,a[j],prcpu[j],pmem,ph);
        }
        //printf("%s\n",process);
    }else
    {
        sprintf(process,"PID=%d  TID=%d  %.2f%%CPU  %.2f%%MEM %s",pid,tid,pcpu,pmem,ph);
        //printf("PID=%d  TID=%d  %.2f%%CPU  %.2f%%MEM %s\n",pid,tid,pcpu,pmem,ph);
    }
    //==================================================
    sprintf(result,"%s%s%s",ccpu,cmem,process);
    string s=result; 
    return s;
}

int main(int argc, char** argv)
{
    int pid=0;
    int tid=0;
    string cpu,mem;
    
    if( argc > 1 )
        pid = atoi(argv[1]);
    else{
         pid = getpid();
    }
    if( argc > 2 )
        tid = atoi(argv[2]);

    //printf("pid=%d,tid=%d\n",pid,tid);

    while(1)
    {
        cout<<"----------------------------"<<endl;
        string s=GetCpuMem(pid,cpu,mem,tid);
        cout<<s<<endl;
    }
    return 0;
}

