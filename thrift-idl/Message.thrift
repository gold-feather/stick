namespace go model

struct message {
	1: required i64 id;
	2: required i8 messageType;
	3: required binary data;
}